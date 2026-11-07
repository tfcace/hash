package shell

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tfcace/hash/internal/editor"
)

// Keys a user types at a confirmation hint (hintAction).
const (
	keyRun     = "\r"   // run, finish, retry
	keyEdit    = "\t"   // edit, copy
	keyReply   = "r"    // reply to an explanation
	keyDecline = "\x1b" // Esc
	keyKillAll = "\x15" // Ctrl+U in an editor: the line goes, the cursor at its end
)

// testTerminal is the shell's terminal in a test: a pty the shell's
// keyboard reads as it reads the user's terminal (stdin and the key reader
// its editors share), and a user who types on the other side. The turn's
// key watcher, its questions and its editors all run as they do for the
// user; nothing is stubbed between the keys and the shell.
type testTerminal struct {
	t    *testing.T
	fd   int      // the shell's side
	keys *os.File // the user's side

	mu         sync.Mutex
	hints      []string // keys for each confirmation hint, in order; Esc once they run out
	permission string   // the key for each permission prompt
}

// newTestTerminal makes a pty the shell's terminal: its stdin and
// sh.editorCfg.Input read it until the test ends.
func newTestTerminal(t *testing.T, sh *Shell) *testTerminal {
	t.Helper()
	tty, fd, keys := openTTY(t)
	useTTY(sh, tty)
	sh.editorCfg.Input = typingSlowly(sh.editorCfg.Input)
	return &testTerminal{t: t, fd: fd, keys: keys, permission: "n"}
}

// typeKeys types keys now.
func (tt *testTerminal) typeKeys(keys string) {
	if _, err := tt.keys.WriteString(keys); err != nil {
		tt.t.Error(err)
	}
}

// answerHints has the user answer each confirmation hint drawn on screen
// with the next of answers (Esc once they run out), and each tool
// permission prompt with the permission key, until the test ends.
func (tt *testTerminal) answerHints(screen *lockedBuffer, answers ...string) {
	tt.mu.Lock()
	tt.hints = append(tt.hints, answers...)
	tt.mu.Unlock()
	done := make(chan struct{})
	stopped := make(chan struct{})
	tt.t.Cleanup(func() { close(done); <-stopped })
	go func() {
		defer close(stopped)
		hints, prompts := 0, 0
		for {
			select {
			case <-done:
				return
			case <-time.After(time.Millisecond):
			}
			out := screen.String()
			if n := strings.Count(out, "Agent wants to run"); n > prompts {
				// The watcher asks for the key before the prompt is drawn.
				prompts = n
				tt.mu.Lock()
				key := tt.permission
				tt.mu.Unlock()
				tt.typeKeys(key)
			}
			if n := countHints(out); n > hints {
				hints = n
				if !tt.waitAsking(done) {
					return
				}
				tt.mu.Lock()
				key := keyDecline
				if len(tt.hints) > 0 {
					key, tt.hints = tt.hints[0], tt.hints[1:]
				}
				tt.mu.Unlock()
				tt.typeKeys(key)
			}
		}
	}()
}

// answerPermissions sets the key the user answers permission prompts with.
func (tt *testTerminal) answerPermissions(key string) {
	tt.mu.Lock()
	tt.permission = key
	tt.mu.Unlock()
}

// countHints counts the confirmation hints drawn in out.
func countHints(out string) int {
	return strings.Count(out, "enter to run") + strings.Count(out, "enter to finish") + strings.Count(out, "enter to retry")
}

// waitAsking waits until a question owns the terminal, raw with signals off
// as keyboard.ask leaves it, and then a moment longer, so the question has
// set aside what was typed before it: a key typed now answers it.
func (tt *testTerminal) waitAsking(done <-chan struct{}) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		termios, err := unix.IoctlGetTermios(tt.fd, ioctlGetTermios)
		if err == nil && termios.Lflag&unix.ISIG == 0 {
			break
		}
		if time.Now().After(deadline) {
			tt.t.Error("a hint was drawn, and no question asked for its key")
			return false
		}
		select {
		case <-done:
			return false
		case <-time.After(time.Millisecond):
		}
	}
	time.Sleep(30 * time.Millisecond)
	return true
}

// typeAhead gives sh a key reader on a pipe, not a terminal, with keys
// already typed: its editors read them as they come, and nothing can ask a
// question on it. It returns the reader, to see what is left on it.
func typeAhead(t *testing.T, sh *Shell, keys string) *editor.InputReader {
	t.Helper()
	reader, typed := typedKeys(t)
	if _, err := typed.WriteString(keys); err != nil {
		t.Fatal(err)
	}
	sh.editorCfg.Input = reader
	return reader
}

// questionDeadline bounds how long code under test may wait for the keys
// that answer its questions. A key the test typed too early (before the
// question set the typeahead aside) leaves the question waiting: the test
// then fails at the deadline instead of hanging until go test's timeout.
const questionDeadline = 10 * time.Second

// questionCtx is the context for code under test that asks the user: it
// ends at questionDeadline, and the test fails if it did.
func questionCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), questionDeadline)
	t.Cleanup(func() {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Error("a question waited out the test's deadline: the key meant for it was missed")
		}
		cancel()
	})
	return ctx
}
