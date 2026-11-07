package shell

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/parser"
)

// Once the shell ends at a hint, the turn draws nothing more and records
// nothing: the ending used to read as a decline, which cleared the hint,
// printed a line and saved the turn as declined.
func TestTurn_ShellEndingAtAHintDrawsAndRecordsNothing(t *testing.T) {
	for name, script := range map[string]turnScript{
		"command": {events: []agent.StreamEvent{textEvent("```bash\nls -a\n```")}},
		"explain": {events: []agent.StreamEvent{textEvent("Files are things on disk. They hold data.")}},
		"retry":   {err: errFlaky},
	} {
		t.Run(name, func(t *testing.T) {
			store, err := history.NewStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			tr := &scriptedTransport{scripts: []turnScript{script}}
			sh, screen, _ := newTurnShellOff(t, tr)
			sh.history = store
			tt := newTestTerminal(t, sh)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stdout := liveStdout(t)
			var before, beforeStdout string
			go func() {
				waitFor(t, func() bool {
					tio, err := unix.IoctlGetTermios(tt.fd, ioctlGetTermios)
					return countHints(screen.String()) > 0 && err == nil && tio.Lflag&unix.ISIG == 0
				})
				time.Sleep(30 * time.Millisecond)
				before, beforeStdout = screen.String(), stdout.String()
				cancel()
			}()
			line := "?? list files"
			_ = sh.handleAgentRequest(ctx, line, parser.Parse(line))
			if after := strings.TrimPrefix(screen.String(), before); after != "" {
				t.Errorf("drawn after the shell ended: %q", after)
			}
			time.Sleep(20 * time.Millisecond) // the copy catches up
			if after := strings.TrimPrefix(stdout.String(), beforeStdout); after != "" {
				t.Errorf("printed after the shell ended: %q", after)
			}
			if turns, err := store.GetAgentInteractions("", 10); err != nil || len(turns) != 0 {
				t.Errorf("recorded after the shell ended: %+v (%v)", turns, err)
			}
		})
	}
}

// liveStdout sends os.Stdout to a buffer the test can read while the code
// under test still writes, until the test ends.
func liveStdout(t *testing.T) *lockedBuffer {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	buf := &lockedBuffer{}
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(buf, r); close(copied) }()
	t.Cleanup(func() {
		os.Stdout = saved
		_ = w.Close()
		<-copied
		_ = r.Close()
	})
	return buf
}
