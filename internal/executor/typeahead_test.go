package executor

import (
	"context"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// splitTerminalReplies drops what a terminal sends back to a query and
// keeps every key: a user's typing, arrows and lone escapes included.
func TestSplitTerminalReplies(t *testing.T) {
	tests := []struct{ in, keys string }{
		{"\x1b[?62c", ""},               // device attributes
		{"\x1b[12;5R", ""},              // cursor position
		{"\x1bP1$r0m\x1b\\", ""},        // DECRQSS
		{"\x1b]11;rgb:0/0/0\x07", ""},   // OSC color, BEL-ended
		{"\x1b]10;rgb:f/f/f\x1b\\", ""}, // OSC color, ST-ended
		{"\x1b[?1u", ""},                // kitty keyboard flags
		{"hello\r", "hello\r"},
		{"\x1b[?62cls\r", "ls\r"},
		{"cat\rhello\r", "cat\rhello\r"},
		{"\x1b[A\x1b[1;5D", "\x1b[A\x1b[1;5D"}, // arrows
		{"\x1b", "\x1b"},
		{"x\x1b[", "x\x1b["}, // cut short: kept
	}
	for _, tt := range tests {
		if got := string(splitTerminalReplies([]byte(tt.in))); got != tt.keys {
			t.Errorf("splitTerminalReplies(%q) = %q, want %q", tt.in, got, tt.keys)
		}
	}
}

// ptyTerminal is a terminal for the executor's stdin and the line's output,
// as the interactive shell has, that the test types into.
type ptyTerminal struct {
	in    *os.File // what the test types into
	out   *os.File // the line's stdout
	stdin *os.File // the shell's stdin (Executor.stdin): the keys typed
}

func newPTYTerminal(t *testing.T) *ptyTerminal {
	t.Helper()
	return newPTYTerminalReadBy(t, func(outMaster *os.File) { _, _ = io.Copy(io.Discard, outMaster) })
}

// newPTYTerminalReadBy is newPTYTerminal, whose screen read reads.
func newPTYTerminalReadBy(t *testing.T, read func(outMaster *os.File)) *ptyTerminal {
	t.Helper()
	inMaster, inSlave, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	t.Cleanup(func() { inMaster.Close(); inSlave.Close() })
	outMaster, outSlave, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	t.Cleanup(func() { outMaster.Close(); outSlave.Close() })
	term := &ptyTerminal{in: inMaster, out: outSlave}
	go read(outMaster)
	// fd 0 is a plain blocking file in a real session (see ttyShellOn). The
	// copy is closed on exec, so the commands of the tests running beside
	// this one do not hold this terminal open.
	dup, err := unix.FcntlInt(inSlave.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	term.stdin = os.NewFile(uintptr(dup), "stdin")
	t.Cleanup(func() { term.stdin.Close() })
	return term
}

// shell is a new interactive shell on the terminal.
func (pt *ptyTerminal) shell() *Executor {
	e := New()
	e.stdin = pt.stdin
	e.terminal = terminalInteractive
	return e
}

// Keys typed ahead of a command that reads the terminal reach it, as in
// bash: `cat⏎hello⏎` typed fast gives cat hello. The drain of terminal
// replies before a PTY command used to read the keys and drop them.
func TestExecute_TypeaheadReachesThePTYCommand(t *testing.T) {
	t.Parallel()
	term := newPTYTerminal(t)
	e := term.shell()
	if _, err := term.in.WriteString("hello\r"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := e.Execute(ctx, `head -n 1`, term.out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil || !strings.Contains(res.CapturedOutput, "hello") {
		t.Fatalf("head did not get the typed line: output %q, canceled %v", res.CapturedOutput, ctx.Err() != nil)
	}
}

// fakeTerminal is a terminal on master that was typed typed, and that
// sends early, the reply to a query from before (empty for none), delay
// later. When it answers it replies, after early (terminals answer in
// order) and a moment later, to the first query the shell sends: device
// attributes or a status report.
func fakeTerminal(t *testing.T, typed, early string, delay time.Duration, answers bool) (slave *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	t.Cleanup(func() { master.Close(); slave.Close() })
	if _, err := term.MakeRaw(int(slave.Fd())); err != nil {
		t.Fatal(err)
	}
	if _, err := master.WriteString(typed); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(delay)
		_, _ = master.WriteString(early)
		if !answers {
			return
		}
		var got []byte
		buf := make([]byte, 64)
		for !strings.Contains(string(got), "\x1b[c") && !strings.Contains(string(got), "\x1b[5n") {
			n, err := master.Read(buf)
			if err != nil {
				return
			}
			got = append(got, buf[:n]...)
		}
		time.Sleep(5 * time.Millisecond)
		if strings.Contains(string(got), "\x1b[5n") {
			_, _ = master.WriteString("\x1b[0n")
		} else {
			_, _ = master.WriteString("\x1b[?62;22c")
		}
	}()
	return slave
}

// The wait for the replies to terminal queries still on their way when a
// PTY command starts ends once the terminal answered a query of the
// shell's (device attributes), which it answers after every earlier one,
// not after a fixed time. Every interactive PTY command used to start 50ms
// late, and a reply slower than that reached the command as input. The
// keys typed ahead stay; the replies go.
func TestDrainTerminalReplies_EndsAtTheTerminalsAnswer(t *testing.T) {
	saved := terminalReplyWait
	t.Cleanup(func() { terminalReplyWait = saved })
	const osc = "\x1b]11;rgb:0/0/0\x1b\\"

	terminalReplyWait = 10 * time.Second
	slave := fakeTerminal(t, "ls", osc, 0, true)
	start := time.Now()
	if keys := drainTerminalReplies(int(slave.Fd()), nil); string(keys) != "ls" {
		t.Errorf("keys = %q, want the typed ls without the replies", keys)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the drain took %v after the terminal answered", took)
	}

	// A slow reply is drained, not left for the command.
	slave = fakeTerminal(t, "", osc, 200*time.Millisecond, true)
	if keys := drainTerminalReplies(int(slave.Fd()), nil); len(keys) != 0 {
		t.Errorf("keys = %q from replies alone", keys)
	}
	if hasDataAvailable(int(slave.Fd()), 500*time.Millisecond) {
		b := make([]byte, 64)
		n, _ := slave.Read(b)
		t.Errorf("a slow terminal's reply %q was left for the command to read", b[:n])
	}

	// A terminal that never answers costs the bounded wait.
	terminalReplyWait = 100 * time.Millisecond
	slave = fakeTerminal(t, "ls", "", 0, false)
	if keys := drainTerminalReplies(int(slave.Fd()), nil); string(keys) != "ls" {
		t.Errorf("keys = %q from a terminal that does not answer, want ls", keys)
	}
}

// A reply to an earlier query of the same kind that is still pending as the
// drain starts does not end it: the drain waits for the answer to its own
// fence, a status report (DSR), which nothing else hash or a program asks
// for. The fence was a device attributes query, whose answer a library
// asks for too; a stale one ended the drain at once and left the fence's
// own reply for the command.
func TestDrainTerminalReplies_AStaleReplyIsNoFence(t *testing.T) {
	saved := terminalReplyWait
	t.Cleanup(func() { terminalReplyWait = saved })
	terminalReplyWait = 10 * time.Second
	slave := fakeTerminal(t, "", "\x1b[?62;22c", 0, true)
	time.Sleep(20 * time.Millisecond) // the stale reply is pending
	if keys := drainTerminalReplies(int(slave.Fd()), nil); len(keys) != 0 {
		t.Errorf("keys = %q from replies alone", keys)
	}
	if hasDataAvailable(int(slave.Fd()), 200*time.Millisecond) {
		b := make([]byte, 64)
		n, _ := slave.Read(b)
		t.Errorf("the fence's own reply %q was left for the command to read", b[:n])
	}
}
