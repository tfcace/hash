package shell

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/programstatus"
)

// inlineKeys runs an inline ?? turn's real editor on a pipe the test types
// into, with the editor's drawing sent to /dev/null.
func inlineKeys(t *testing.T) (keys *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	stdin, stdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = r, null
	t.Cleanup(func() {
		os.Stdin, os.Stdout = stdin, stdout
		w.Close()
		r.Close()
		null.Close()
	})
	return w
}

// waitStatus waits for the last OSC 7501 report to be want.
func waitStatus(t *testing.T, status *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for lastStatus(status) != want {
		if time.Now().After(deadline) {
			t.Fatalf("last status = %q, want %q", lastStatus(status), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Once the fill has arrived the agent is done: while the user edits, the
// terminal must not still say hash is working, nor show the progress bar.
func TestInlineTurn_FillEndsTheWorkBeforeTheUserActs(t *testing.T) {
	keys := inlineKeys(t)
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("-la")}}}}
	sh, screen, status := newTurnShell(t, tr)
	sh.executor.RegisterBuiltin("probe", func(context.Context, []string, io.Writer, io.Writer) error { return nil })

	done := make(chan error)
	go func() { done <- ask(t, sh, "probe ?? long listing") }()

	waitStatus(t, status, rootReport(programstatus.Blocked, programstatus.Permission, "probe -la"))
	if !strings.Contains(screen.String(), "\x1b]9;4;0") {
		t.Errorf("progress bar still up while the user looks at the fill: %q", screen.String())
	}
	keys.Write([]byte("\r"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sh.last.command != "probe -la" || sh.last.exitCode != 0 {
		t.Errorf("last = %+v, want the accepted line", sh.last)
	}
}

// A failed inline request is a failed line, as a failed full turn is: the
// error status stays and the line exits 1 when the user gives up on it.
func TestInlineTurn_FailedRequestFailsTheLine(t *testing.T) {
	keys := inlineKeys(t)
	tr := &scriptedTransport{scripts: []turnScript{{err: errFlaky}}}
	sh, _, status := newTurnShell(t, tr)

	done := make(chan error)
	go func() { done <- ask(t, sh, "ls ?? long listing") }()

	waitStatus(t, status, rootReport(programstatus.Error, "", errFlaky.Error()))
	keys.Write([]byte{0x03, 0x03}) // clear the line, then leave it
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sh.last.exitCode != 1 {
		t.Errorf("line exit = %d, want 1 for a failed request", sh.last.exitCode)
	}
	if got := lastStatus(status); !strings.Contains(got, "state=error") {
		t.Errorf("last status = %q, want the error kept", got)
	}
}

// Ctrl+C while the fill streams cancels the request: the line exits 1, as
// a canceled full turn does.
func TestInlineTurn_CtrlCWhileStreamingCancelsTheLine(t *testing.T) {
	keys := inlineKeys(t)
	tr := &scriptedTransport{scripts: []turnScript{{waitCtx: true}}}
	sh, _, status := newTurnShell(t, tr)

	done := make(chan error)
	go func() { done <- ask(t, sh, "ls ?? long listing") }()

	waitStatus(t, status, rootReport(programstatus.Working, "", "long listing"))
	keys.Write([]byte{0x03})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sh.last.exitCode != 1 {
		t.Errorf("line exit = %d, want 1 for a canceled request", sh.last.exitCode)
	}
	if want := rootReport(programstatus.Idle, "", ""); lastStatus(status) != want {
		t.Errorf("last status = %q, want idle", lastStatus(status))
	}
}

// A ?? the agent's fill brings (a glob of two-character names) is shell syntax once the user runs
// the line, as in an agent command the user edits; it used to go back to
// the agent as a new inline request.
func TestInlineTurn_AgentFillQuestionMarksAreShellSyntax(t *testing.T) {
	keys := inlineKeys(t)
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("?? -l")}}}}
	sh, _, status := newTurnShell(t, tr)
	var args []string
	sh.executor.RegisterBuiltin("probe", func(_ context.Context, a []string, _, _ io.Writer) error {
		args = a
		return nil
	})

	done := make(chan error)
	go func() { done <- ask(t, sh, "probe ?? two-character names, long") }()
	waitStatus(t, status, rootReport(programstatus.Blocked, programstatus.Permission, "probe ?? -l"))
	keys.Write([]byte("\r"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		keys.Write([]byte{0x03, 0x03, 0x03})
		<-done
		t.Fatalf("the line went back to the agent as request %d instead of running", tr.requestCount())
	}
	if got := tr.requestCount(); got != 1 {
		t.Errorf("requests = %d, want the fill's ?? kept out of the agent", got)
	}
	if len(args) != 2 || args[0] != "??" || args[1] != "-l" {
		t.Errorf("probe ran with %q, want [?? -l]", args)
	}
}

// An inline turn's reply is ghost text in the editor, which owns stdin and
// the screen. A tool permission request during it is denied without a
// prompt (none could be drawn or answered beside the editor), no spinner
// starts under the editor, and the ghost says why the fill stopped.
func TestInlineTurn_ToolPermissionIsDeniedWithANotice(t *testing.T) {
	keys := inlineKeys(t)
	gate := make(chan struct{})
	var allowed, always bool
	var sh *Shell
	tr := &scriptedTransport{scripts: []turnScript{{
		events: []agent.StreamEvent{toolEvent("t1", "ls", agent.ToolCallInProgress)},
		during: func(ctx context.Context) {
			defer close(gate)
			allowed, always = sh.handleToolPermission(ctx, agent.ToolPermissionRequest{Command: "ls", ToolName: "Bash", ToolCallID: "t1"})
		},
		gate:  gate,
		after: []agent.StreamEvent{textEvent("-la")},
	}}}
	sh, screen, status := newTurnShell(t, tr)

	done := make(chan error)
	go func() { done <- ask(t, sh, "ls ?? long listing") }()

	waitStatus(t, status, rootReport(programstatus.Error, "", inlineToolDenied("ls").Error()))
	<-gate
	if allowed || always {
		t.Errorf("permission = %v, %v; want denied in an inline turn", allowed, always)
	}
	if out := screen.String(); strings.Contains(out, "Agent wants to run") || strings.Contains(out, "agent · running") {
		t.Errorf("the turn drew over the inline editor: %q", out)
	}
	keys.Write([]byte{0x03, 0x03}) // clear the line, then leave it
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sh.last.exitCode != 1 {
		t.Errorf("line exit = %d, want 1: the fill never came", sh.last.exitCode)
	}
}

// Ctrl+D in the inline editor leaves the line, as in the editor for an
// agent command: it is not the prompt, so it does not end the shell.
func TestInlineTurn_CtrlDLeavesTheLine(t *testing.T) {
	keys := inlineKeys(t)
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("-la")}}}}
	sh, _, status := newTurnShell(t, tr)

	done := make(chan error)
	go func() { done <- ask(t, sh, "ls ?? long listing") }()
	waitStatus(t, status, rootReport(programstatus.Blocked, programstatus.Permission, "ls -la"))
	keys.Write([]byte{0x03}) // clear the line
	time.Sleep(50 * time.Millisecond)
	keys.Write([]byte{0x04}) // Ctrl+D on the empty line
	if err := <-done; err != nil {
		t.Fatalf("Ctrl+D in the inline editor returned %v, want the line left", err)
	}
}

// An inline line's status says whether the agent's work finished, whatever
// key left the line: 1 when the request failed or was cut short before the
// fill was complete (Ctrl+C or typing over the ghost while it streamed, a
// tool the inline form cannot ask for), 0 when the fill arrived and the user
// declined it, and the command's own status when a line ran.
func TestInlineTurn_TheLineStatusFollowsTheRequest(t *testing.T) {
	filled := func() turnScript { return turnScript{events: []agent.StreamEvent{textEvent("-la")}} }
	hung := func() turnScript { return turnScript{waitCtx: true} }
	ready := rootReport(programstatus.Blocked, programstatus.Permission, "probe -la")
	working := rootReport(programstatus.Working, "", "long listing")
	idle := rootReport(programstatus.Idle, "", "")
	for _, tt := range []struct {
		name   string
		script turnScript
		steps  []string // a status to wait for ("=..."), or keys to type
		want   int
		ran    bool
	}{
		{"fill run", filled(), []string{"=" + ready, "\r"}, 0, true},
		{"fill run, failing", filled(), []string{"=" + ready, "; (exit 2)\r"}, 2, true},
		{"fill declined with Ctrl+C", filled(), []string{"=" + ready, "\x03"}, 0, false},
		{"fill declined with Esc", filled(), []string{"=" + ready, "\x1b"}, 0, false},
		{"fill declined with Ctrl+D", filled(), []string{"=" + ready, "\x15", "\x04"}, 0, false},
		{"fill declined with Enter on an empty line", filled(), []string{"=" + ready, "\x15", "\r"}, 0, false},
		{"Ctrl+C while streaming", hung(), []string{"=" + working, "\x03"}, 1, false},
		{"typed over while streaming, then left", hung(), []string{"=" + working, "x", "=" + idle, "\x03"}, 1, false},
		{"typed over while streaming, then run", hung(), []string{"=" + working, "-x", "=" + idle, "\r"}, 0, true},
		{"failed, then left", turnScript{err: errFlaky}, []string{"=" + rootReport(programstatus.Error, "", errFlaky.Error()), "\x03"}, 1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			keys := inlineKeys(t)
			sh, _, status := newTurnShell(t, &scriptedTransport{scripts: []turnScript{tt.script}})
			ran := false
			sh.executor.RegisterBuiltin("probe", func(context.Context, []string, io.Writer, io.Writer) error {
				ran = true
				return nil
			})
			done := make(chan error)
			go func() { done <- ask(t, sh, "probe ?? long listing") }()
			for _, step := range tt.steps {
				if want, ok := strings.CutPrefix(step, "="); ok {
					waitStatus(t, status, want)
					continue
				}
				keys.Write([]byte(step))
				time.Sleep(30 * time.Millisecond)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the inline line did not end")
			}
			if sh.last.exitCode != tt.want || ran != tt.ran {
				t.Errorf("line exit = %d, ran = %v; want %d, %v", sh.last.exitCode, ran, tt.want, tt.ran)
			}
		})
	}
}

// Declining what the agent offers in its editor, an inline fill or a
// command to edit, takes one key, Esc or Ctrl+C, whatever the line holds:
// the turn ends and the prompt comes back. Esc did nothing there and a
// first Ctrl+C only cleared the line, which left the user in the turn's
// editor: it looked like the prompt, but Ctrl+R did nothing, the status
// still said hash waited for them, and the next line ran as the turn's.
func TestAgentTextEditors_OneKeyDeclines(t *testing.T) {
	t.Parallel()
	for key, keys := range map[string]string{"esc": "\x1b", "ctrl+c": "\x03"} {
		for name, run := range map[string]func(*testing.T, *Shell) error{
			"edit": func(_ *testing.T, sh *Shell) error {
				_, err := sh.handleEditCommand(sh.beginTurn(context.Background()), "probe")
				return err
			},
			"inline": func(t *testing.T, sh *Shell) error { return ask(t, sh, "probe ?? anything") },
		} {
			t.Run(key+"/"+name, func(t *testing.T) {
				tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent(" now")}}}}
				sh, _, status := newTurnShell(t, tr)
				var typed *os.File
				sh.editorCfg.Input, typed = typedKeys(t)
				ran := false
				sh.executor.RegisterBuiltin("probe", func(context.Context, []string, io.Writer, io.Writer) error {
					ran = true
					return nil
				})
				done := make(chan error, 1)
				// stdout is swapped here, never while the editor runs.
				captureStdout(t, sh, func() {
					go func() { done <- run(t, sh) }()
					if name == "inline" {
						waitStatus(t, status, rootReport(programstatus.Blocked, programstatus.Permission, "probe now"))
					} else {
						time.Sleep(100 * time.Millisecond)
					}
					typed.WriteString(keys)
					select {
					case err := <-done:
						if err != nil {
							t.Error(err)
						}
					case <-time.After(3 * time.Second):
						typed.WriteString("\x03\x03") // let the editor go
						<-done
						t.Errorf("%s left the user in the turn's editor", key)
					}
				})
				if ran {
					t.Errorf("%s ran the line", key)
				}
			})
		}
	}
}
