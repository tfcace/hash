package executor

import (
	"bytes"
	"context"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// terminalShows runs line in the interactive shell on one terminal (stdin,
// stdout and stderr, as a person's) and returns what the terminal showed,
// with the keys typed once the line runs.
func terminalShows(t *testing.T, line, keys string) string {
	t.Helper()
	e := New()
	e.terminal = terminalInteractive
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	var mu sync.Mutex
	var shown bytes.Buffer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			shown.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	// fd 0 as a real session has it: a plain blocking file (see ttyShellAt).
	dup, err := unix.FcntlInt(slave.Fd(), unix.F_DUPFD_CLOEXEC, 0) // closed on exec: tests run side by side
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	stdin := os.NewFile(uintptr(dup), "stdin")
	defer stdin.Close()
	e.stdin = stdin
	if keys != "" {
		time.AfterFunc(500*time.Millisecond, func() { _, _ = io.WriteString(master, keys) })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := e.Execute(ctx, line, slave, slave); err != nil {
		t.Fatalf("Execute(%q): %v", line, err)
	}
	time.Sleep(200 * time.Millisecond) // the terminal shows the last of it
	mu.Lock()
	defer mu.Unlock()
	return strings.ReplaceAll(shown.String(), "\r", "")
}

// A pipeline's tail writes its stdout and its stderr to the terminal in
// the order it wrote them, with and without 2>&1, as in any shell. Its
// stdout used to reach the terminal through a PTY hash copied from, and its
// stderr straight: every E line came before the O lines.
func TestExecute_PipelineTailKeepsItsOutputInOrder(t *testing.T) {
	t.Parallel()
	const tail = `sh -c 'while read n; do echo O$n; echo E$n >&2; sleep 0.05; done'`
	for _, line := range []string{"seq 3 | " + tail, "seq 3 | " + tail + " 2>&1"} {
		got := terminalShows(t, line, "")
		if want := "O1\nE1\nO2\nE2\nO3\nE3\n"; !strings.Contains(got, want) {
			t.Errorf("%q: the terminal showed %q, want %q in order", line, got, want)
		}
	}
}
