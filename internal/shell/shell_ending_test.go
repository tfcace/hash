package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/programstatus"
	"github.com/tfcace/hash/internal/prompt"
)

// barPrompt gives sh a two-row prompt whose info bar is BAR, the way
// starship draws one above the input line.
func barPrompt(t *testing.T, sh *Shell) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "starship")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'BAR\\n> '\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh.prompt = prompt.New(prompt.Config{Mode: "starship", StarshipPath: script})
}

// A fatal signal (SIGTERM, SIGHUP) ends the shell's context while the user
// edits agent text: the editor ends with the context's error, which is the
// shell ending, not an editor error, and the line draws no fresh prompt bar
// before Run sees the context done.
func TestEnding_AnEditorTheShellEndsDrawsNothing(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		line string
		up   func(status string) bool // the editor is up, or about to be
	}{
		"inline": {"ls ?? show hidden", func(status string) bool {
			return strings.Contains(status, rootReport(programstatus.Working, "", "show hidden"))
		}},
		"edit": {"?? list files", func(status string) bool {
			return strings.HasSuffix(status, rootReport(programstatus.Idle, "", "")) // the hint's Tab let go of the turn
		}},
	} {
		t.Run(name, func(t *testing.T) {
			tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("```bash\nls -a\n```")}}}}
			sh, screen, status := newTurnShell(t, tr, keyEdit)
			barPrompt(t, sh)
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				waitFor(t, func() bool { return tc.up(status.String()) })
				time.Sleep(50 * time.Millisecond)
				cancel() // SIGTERM while the editor is up
			}()
			out := captureStdout(t, sh, func() { _ = sh.handleLine(ctx, tc.line) })
			if strings.Contains(screen.String()+out, "editor error") {
				t.Errorf("the shell ending drew an editor error: %q", screen.String()+out)
			}
			if strings.Contains(out, "BAR") {
				t.Errorf("the shell ending drew a prompt bar: %q", out)
			}
		})
	}
}

// A request the shell's ending cancels was not canceled by the user: no
// "canceled" row is drawn on the way out.
func TestEnding_ARequestTheShellEndsDrawsNoCanceledRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tr := &scriptedTransport{scripts: []turnScript{{
		events:  []agent.StreamEvent{textEvent("Working on it")},
		during:  func(context.Context) { time.Sleep(20 * time.Millisecond); cancel() },
		waitCtx: true,
	}}}
	sh, screen, _ := newTurnShell(t, tr)
	out := captureStdout(t, sh, func() { _ = sh.handleLine(ctx, "?? count files") })
	if drawn := screen.String() + out; strings.Contains(drawn, "canceled") {
		t.Errorf("the shell ending drew a canceled row: %q", drawn)
	}
}
