package editor

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// openPty opens a terminal pair: the editor reads the slave (put in raw mode
// by Run like a real prompt), the test types on the master.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { master.Close(); slave.Close() })
	go io.Copy(io.Discard, master) //nolint:errcheck // drains the echo until the test closes the pty
	return master, slave
}

// runPrompt runs one prompt the way the shell does: a new editor on the
// shell's one reader.
func runPrompt(t *testing.T, r *InputReader, slave *os.File, timeout time.Duration, ready func()) (Result, *Editor, error) {
	t.Helper()
	ed := New(Config{Input: r, OnInputReady: ready}, slave, io.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	res, err := ed.Run(ctx)
	return res, ed, err
}

// Two lines typed while a command ran: the first prompt takes the first,
// the second prompt the second. Each prompt used to drain the typeahead into
// a reader of its own and drop what it did not use.
func TestEditors_TypeaheadOfTwoLines(t *testing.T) {
	master, slave := openPty(t)
	master.WriteString("echo one\recho two\r")
	time.Sleep(50 * time.Millisecond)

	r := NewInputReader(slave)
	for _, want := range []string{"echo one", "echo two"} {
		res, _, err := runPrompt(t, r, slave, time.Second, nil)
		if err != nil || res.Exit != ExitSubmit || res.Text != want {
			t.Fatalf("prompt = %q (exit %v, err %v), want %q submitted", res.Text, res.Exit, err, want)
		}
	}
}

// Two lines typed in one burst while the first prompt is live: the keys
// the first editor's reader took past its Enter go to the second prompt.
// They used to be dropped at teardown ("cho two").
func TestEditors_LiveBurstKeepsKeysPastEnter(t *testing.T) {
	for run := range 20 {
		master, slave := openPty(t)
		r := NewInputReader(slave)
		res, _, err := runPrompt(t, r, slave, time.Second, func() {
			master.WriteString("echo one\recho two\r")
		})
		if err != nil || res.Text != "echo one" {
			t.Fatalf("run %d: first prompt = %q (err %v)", run, res.Text, err)
		}
		res, _, err = runPrompt(t, r, slave, time.Second, nil)
		if err != nil || res.Exit != ExitSubmit || res.Text != "echo two" {
			t.Fatalf("run %d: second prompt = %q (exit %v, err %v), want \"echo two\"", run, res.Text, res.Exit, err)
		}
	}
}

// ttyBytes reads what a program started after the prompt would read from
// the terminal (in raw mode, as ssh puts it).
func ttyBytes(t *testing.T, slave *os.File) string {
	t.Helper()
	if st, err := term.MakeRaw(int(slave.Fd())); err == nil {
		defer term.Restore(int(slave.Fd()), st) //nolint:errcheck
	}
	var got []byte
	buf := make([]byte, 64)
	for hasDataAvailableWithTimeout(int(slave.Fd()), 20*time.Millisecond) {
		n, err := slave.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	return string(got)
}

// Lines typed ahead past the one a prompt submits are the next program's
// input, as in bash: `ssh host⏎uptime⏎` runs uptime on the host. The
// editor used to queue all typeahead at startup and read a key past its
// Enter, so uptime ran locally at the next prompt.
func TestEditors_TypeaheadPastEnterIsLeftForTheCommand(t *testing.T) {
	master, slave := openPty(t)
	master.WriteString("ssh host\ruptime\r")
	time.Sleep(50 * time.Millisecond)

	r := NewInputReader(slave)
	res, _, err := runPrompt(t, r, slave, time.Second, nil)
	if err != nil || res.Text != "ssh host" {
		t.Fatalf("prompt = %q (err %v), want \"ssh host\"", res.Text, err)
	}
	if len(r.unreadKeys) > 0 || len(r.queue) > 0 {
		t.Errorf("reader holds keys %v and bytes %q past the Enter", r.unreadKeys, r.queue)
	}
	if got := ttyBytes(t, slave); got != "uptime\n" {
		t.Errorf("the command reads %q, want \"uptime\\n\"", got)
	}
}

// An Esc typed in one burst with a ?? line's Enter is for the turn that
// line starts: it stays on the terminal for the turn's key watcher. The
// editor's feed used to read it past the Enter and hold it.
func TestEditors_KeyTypedWithTheEnterStaysOnTheTerminal(t *testing.T) {
	for run := range 10 {
		master, slave := openPty(t)
		r := NewInputReader(slave)
		res, _, err := runPrompt(t, r, slave, time.Second, func() { master.WriteString("?? q\r\x1b") })
		if err != nil || res.Text != "?? q" {
			t.Fatalf("run %d: prompt = %q (err %v)", run, res.Text, err)
		}
		if len(r.unreadKeys) > 0 || len(r.queue) > 0 {
			t.Fatalf("run %d: reader holds keys %v and bytes %q past the Enter", run, r.unreadKeys, r.queue)
		}
		if got := ttyBytes(t, slave); got != "\x1b" {
			t.Fatalf("run %d: terminal holds %q, want the Esc", run, got)
		}
	}
}

// Typeahead takes the keys given back and those already typed, in order,
// without waiting for more.
func TestInputReader_TypeaheadTakesWhatIsTyped(t *testing.T) {
	r, w := pipeReader(t)
	r.UnreadKeys(Key{Rune: 'a'})
	w.WriteString("b\x1b[A")
	time.Sleep(10 * time.Millisecond)
	got := r.Typeahead()
	want := []Key{{Rune: 'a'}, {Rune: 'b'}, {Special: KeyUp}}
	if !keysEqual(got, want) {
		t.Fatalf("Typeahead() = %+v, want %+v", got, want)
	}
	if again := r.Typeahead(); len(again) != 0 {
		t.Errorf("second Typeahead() = %+v, want nothing", again)
	}
}

// Ctrl+R hands the terminal to bubbletea's history picker: keys typed after
// it are the picker's, so the editor's reader must hold none of them.
func TestEditors_HandOffToAPickerHoldsNothing(t *testing.T) {
	master, slave := openPty(t)
	r := NewInputReader(slave)
	res, _, err := runPrompt(t, r, slave, time.Second, func() { master.WriteString("\x12abc") })
	if err != nil || res.Exit != ExitHistorySearch {
		t.Fatalf("prompt = %+v (err %v), want the history search", res, err)
	}
	if len(r.unreadKeys) > 0 || len(r.queue) > 0 {
		t.Errorf("reader holds keys %v and bytes %q for the picker", r.unreadKeys, r.queue)
	}
	if got := ttyBytes(t, slave); got != "abc" {
		t.Errorf("the picker reads %q, want \"abc\"", got)
	}
}
