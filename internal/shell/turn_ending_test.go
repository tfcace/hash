package shell

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/parser"
)

// stdoutTo sends os.Stdout to out until the test ends.
func stdoutTo(t *testing.T, out *lockedBuffer) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	copied := make(chan struct{})
	go func() { defer close(copied); _, _ = io.Copy(out, r) }()
	t.Cleanup(func() {
		os.Stdout = saved
		_ = w.Close()
		<-copied
		_ = r.Close()
	})
}

// asking reports whether a question owns the terminal fd: raw, signals off.
func asking(fd int) bool {
	tio, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	return err == nil && tio.Lflag&unix.ISIG == 0
}

// The shell ending (SIGTERM, the terminal closing) while a hint waits for
// its key ends the turn as it stands: nothing more is drawn (no hint
// cleared, no blank line after it), the reply is not recorded as declined,
// and a failed turn is not offered its retry again. The keyboard's ending
// read as Esc, so the turn went on as declined.
func TestTurn_TheShellEndingAtAHintDrawsAndRecordsNothing(t *testing.T) {
	for name, script := range map[string]turnScript{
		"command": {events: []agent.StreamEvent{textEvent("```bash\nls -a\n```")}},
		"explain": {events: []agent.StreamEvent{textEvent("Files are things on disk. They hold data.")}},
		"retry":   {err: errFlaky},
	} {
		t.Run(name, func(t *testing.T) {
			tr := &scriptedTransport{scripts: []turnScript{script}}
			sh, screen, _ := newTurnShellOff(t, tr)
			store, err := history.NewStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			sh.history = store
			tt := newTestTerminal(t, sh)
			var stdout lockedBuffer
			stdoutTo(t, &stdout)

			ctx, endShell := context.WithCancel(context.Background())
			defer endShell()
			var screenBefore, stdoutBefore string
			snapped := make(chan struct{})
			go func() {
				defer close(snapped)
				waitFor(t, func() bool { return countHints(screen.String()) > 0 && asking(tt.fd) })
				screenBefore, stdoutBefore = screen.String(), stdout.String()
				endShell()
			}()
			line := "?? list files"
			_ = sh.handleAgentRequest(ctx, line, parser.Parse(line))
			<-snapped
			if after := screen.String()[len(screenBefore):]; after != "" {
				t.Errorf("drew %q on the screen after the shell ended", after)
			}
			if after := stdout.String()[len(stdoutBefore):]; after != "" {
				t.Errorf("wrote %q after the shell ended", after)
			}
			if got, _ := store.GetAgentInteractions("", 10); len(got) > 0 {
				t.Errorf("recorded %+v after the shell ended", got)
			}
			if n := tr.requestCount(); n != 1 {
				t.Errorf("%d requests, want the one: no retry after the shell ended", n)
			}
		})
	}
}

// The shell ending while a ?? pipe's command runs ends the turn there: no
// request goes to the agent with what the cut-off command printed, and
// Ctrl+C there cancels the turn the same way. The command is a line of the
// shell: Ctrl+C interrupts it as one at the prompt, so it waits in hash (a
// loop), where the shell's SIGINT alone stops it; an external command
// would stop only on the terminal's SIGINT to it too.
func TestTurn_EndingDuringThePipeCommandSendsNoRequest(t *testing.T) {
	for name, stop := range map[string]func(sh *Shell, endShell func()){
		"shell ends": func(_ *Shell, endShell func()) { endShell() },
		"Ctrl+C":     func(sh *Shell, _ func()) { sh.interrupts.fire() },
	} {
		t.Run(name, func(t *testing.T) {
			tr := &scriptedTransport{}
			sh, _, _ := newTurnShell(t, tr)
			started := filepath.Join(t.TempDir(), "started")
			sh.executor.SetExportedEnv("STARTED", started)
			ctx, endShell := context.WithCancel(context.Background())
			defer endShell()
			go func() {
				waitFor(t, func() bool { _, err := os.Stat(started); return err == nil })
				stop(sh, endShell)
			}()
			out := captureStdout(t, sh, func() { _ = sh.handleLine(ctx, `{ : >"$STARTED"; while :; do :; done; } | ?? summarize`) })
			if n := tr.requestCount(); n != 0 {
				t.Errorf("%d requests went to the agent after the pipe command was cut off", n)
			}
			// Nothing is drawn for the turn: neither ending (the shell is
			// going) nor Ctrl+C (the line it interrupted draws nothing)
			// is the user canceling a request.
			if strings.Contains(out, "canceled") || strings.Contains(out, "[agent") {
				t.Errorf("printed %q, want nothing of the turn", out)
			}
		})
	}
}
