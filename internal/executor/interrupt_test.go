package executor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// endedProcess runs a shell command and returns how it ended.
func endedProcess(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	_ = cmd.Run()
	return cmd
}

// A SIGINT with no foreground command ends the shell now; with one, the
// shell ends only if it dies of the signal, and the SIGINT is forgotten
// once the commands that saw it ended.
func TestForeground_Interrupt(t *testing.T) {
	handled := endedProcess(t, "exit 3")
	killed := endedProcess(t, "kill -INT $$")

	var f foreground
	ends := 0
	end := func() { ends++ }
	if taken, _ := f.interrupt(end); taken {
		t.Fatal("interrupt with no command running = true, want false (end now)")
	}

	f.start()
	if taken, _ := f.interrupt(end); !taken {
		t.Fatal("interrupt while a command runs = false, want true")
	}
	f.end(handled.ProcessState)
	if ends != 0 {
		t.Fatalf("a command that handled SIGINT ended the shell")
	}
	f.start()
	f.end(killed.ProcessState) // a later command's own SIGINT
	if ends != 0 {
		t.Fatalf("a SIGINT from before the command ended the shell")
	}

	// A pipeline whose commands all die of it ends the shell, once its
	// last command ended.
	f.start()
	f.start()
	f.interrupt(end)
	f.end(killed.ProcessState)
	if ends != 0 {
		t.Fatalf("the shell ended while a pipeline command still ran")
	}
	f.end(killed.ProcessState)
	if ends != 1 {
		t.Fatalf("ends = %d after the pipeline's commands died of SIGINT, want 1", ends)
	}
	if f.state() != batchIdle {
		t.Fatalf("state %v after the commands ended, want idle", f.state())
	}
}

// Ctrl+C reaches every command of a pipeline. bash waits for all of them
// and goes on when one handled the signal (ended other than by it), in any
// position: `sleep 10 | handler` runs the handler to its end, and the
// script after it. hash used to end at the first command's death, killing
// the rest (a pager left the terminal with its echo off).
func TestForeground_PipelineDecidesWhenItsLastCommandEnds(t *testing.T) {
	handled := endedProcess(t, "exit 0")
	killed := endedProcess(t, "kill -INT $$")
	for _, tc := range []struct {
		name  string
		order []*exec.Cmd
		ends  int
	}{
		{"dies, then the handler ends", []*exec.Cmd{killed, handled}, 0},
		{"the handler ends, then one dies", []*exec.Cmd{handled, killed}, 0},
		{"all die", []*exec.Cmd{killed, killed}, 1},
	} {
		var f foreground
		ends := 0
		end := func() { ends++ }
		f.start()
		f.start()
		f.interrupt(end)
		f.end(tc.order[0].ProcessState)
		if ends != 0 {
			t.Errorf("%s: the shell ended while a pipeline command still ran", tc.name)
		}
		f.end(tc.order[1].ProcessState)
		if ends != tc.ends {
			t.Errorf("%s: ends = %d once the pipeline ended, want %d", tc.name, ends, tc.ends)
		}
	}
}

// A pipeline's tail that writes to the terminal is a foreground command
// too: it shares the shell's terminal, so Ctrl+C reaches it, and its end
// decides with the others. It used to go uncounted (on a PTY then): a
// SIGINT while it ran found no command running and ended the line under it.
func TestExecutor_InterruptLineCountsAPipelinesTail(t *testing.T) {
	t.Parallel()
	ready := filepath.Join(t.TempDir(), "ready")
	e := New()
	done := make(chan string, 1)
	go func() {
		done <- ttyShellOn(t, e, `true | sh -c ': > `+ready+`; sleep 0.5; echo tail-done'`)
	}()
	for _, err := os.Stat(ready); err != nil; _, err = os.Stat(ready) {
		time.Sleep(5 * time.Millisecond)
	}
	ended := false
	if !e.InterruptLine(func() { ended = true }) {
		t.Error("InterruptLine while the pipeline's tail runs = false, want true (it decides)")
	}
	if out := <-done; !strings.Contains(out, "tail-done") || ended {
		t.Errorf("output %q, ended %v; want the tail to run to its end and the line to go on", out, ended)
	}
}

// Ctrl+C at a line that runs in hash (a loop, a read of the terminal)
// finds no command to decide: InterruptLine reports false and the caller
// cancels the line, which ends it at once, read included.
func TestExecutor_InterruptLineWithNothingRunningEndsTheLine(t *testing.T) {
	for _, line := range []string{"echo waiting; while :; do :; done", "echo waiting; read Y", "echo waiting; read -s -p 'pw: ' Y"} {
		e := New()
		stdinOnTTY(t)
		ctx, cancel := context.WithCancel(context.Background())
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := e.Execute(ctx, line, w, io.Discard)
			w.Close()
			done <- err
		}()
		buf := make([]byte, len("waiting"))
		if _, err := io.ReadFull(r, buf); err != nil {
			t.Fatal(err)
		}
		if e.InterruptLine(cancel) {
			t.Errorf("%s: InterruptLine = true with no command running", line)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: the canceled line did not end", line)
		}
		r.Close()
	}
}

// stdinOnTTY makes os.Stdin a terminal for the test, wrapped as a real
// session's fd 0 is (a blocking file without deadlines).
func stdinOnTTY(t *testing.T) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	dup, err := syscall.Dup(int(slave.Fd()))
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	stdin := os.NewFile(uintptr(dup), "stdin")
	orig := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = orig
		stdin.Close()
		slave.Close()
		master.Close()
	})
}

// A read from a pipe hash opened itself (a FIFO) stays cancelable when its
// writer stops halfway through a line: the interpreter cancels it with a
// read deadline. Asking whether the file was a terminal used to put it in
// blocking mode (os.File.Fd), and the canceled line then waited for the
// writer forever.
func TestExecute_CanceledReadOfAStalledFIFOEnds(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	w, err := os.OpenFile(fifo, os.O_RDWR, 0) // holds it open: no EOF
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.SetReadDeadline(time.Time{}) != nil {
		// darwin: Go cannot poll a FIFO, and a read of a stalled one cannot
		// be canceled at all (the wait before the read ends at its first byte).
		t.Skip("FIFOs take no read deadlines on this system")
	}
	if _, err := w.WriteString("abc"); err != nil { // no newline: the read waits
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = New().Execute(ctx, `read x < `+fifo, io.Discard, io.Discard)
		close(done)
	}()
	time.AfterFunc(200*time.Millisecond, cancel)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the canceled read of a stalled FIFO did not end")
	}
}

// ptyLine runs line in the interactive shell's terminal mode, on a
// terminal the test types into (master), and routes SIGINT as the REPL
// does: to InterruptLine, with an end that cancels the line. It returns
// a channel closed once the line ended.
func ptyLine(t *testing.T, line string) (master *os.File, done <-chan struct{}) {
	t.Helper()
	e := New()
	e.terminal = terminalInteractive
	e.WatchInterrupts()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	outMaster, outSlave, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	go io.Copy(io.Discard, outMaster) //nolint:errcheck // drain the terminal
	dup, err := syscall.Dup(int(slave.Fd()))
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	stdin := os.NewFile(uintptr(dup), "stdin")
	orig := os.Stdin
	os.Stdin = stdin

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for range sigs {
			if !e.InterruptLine(cancel) {
				cancel()
			}
		}
	}()
	ended := make(chan struct{})
	go func() {
		_, _ = e.Execute(ctx, line, outSlave, io.Discard)
		close(ended)
	}()
	t.Cleanup(func() {
		cancel()
		<-ended
		signal.Stop(sigs)
		close(sigs)
		os.Stdin = orig
		for _, f := range []*os.File{stdin, slave, master, outSlave, outMaster} {
			f.Close()
		}
	})
	return master, ended
}

// At the interactive shell a command whose stdin is the terminal runs on a
// PTY of its own, with the user's terminal raw: Ctrl+C reaches only the
// command, and hash gets no SIGINT. A command that dies of it is a Ctrl+C
// for the line all the same, as in bash: `for i in 1 2 3; do sleep 2;
// done` ends at the first Ctrl+C. One that handles it lets the line go on.
// The loop used to go on after every Ctrl+C.
func TestExecute_PTYCommandDyingOfCtrlCEndsTheLine(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		ends      bool
	}{
		{"dies of it", `sh -c ": > $R$i; sleep 5"`, true},
		{"handles it", `sh -c "trap 'exit 0' INT; : > $R$i; sleep 5 & wait"`, false},
	} {
		dir := t.TempDir()
		ready := filepath.Join(dir, "ready")
		master, done := ptyLine(t, `R=`+ready+`; for i in 1 2; do `+tc.cmd+`; done`)
		waitForPath(t, ready+"1")
		if _, err := master.Write([]byte{0x03}); err != nil {
			t.Fatal(err)
		}
		if tc.ends {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatalf("%s: the line went on after its command died of Ctrl+C", tc.name)
			}
			if _, err := os.Stat(ready + "2"); err == nil {
				t.Errorf("%s: the loop ran again", tc.name)
			}
			continue
		}
		waitForPath(t, ready+"2")
	}
}

// waitForPath waits for path to exist, under a deadline.
func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for _, err := os.Stat(path); err != nil; _, err = os.Stat(path) {
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A command on a PTY of its own that dies of Ctrl+C ends its line from
// the executor, with status 130, whoever routes the shell's SIGINT: hash
// gets no SIGINT for it. It used to send itself one, which counted on Go
// delivering it within lateSIGINT and on the shell's claim of the line;
// here nobody routes it, and the line went on.
func TestExecute_PTYCommandDyingOfCtrlCEndsTheLineWithNoSignal(t *testing.T) {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT) // taken and never routed
	defer signal.Stop(sigs)
	e := New()
	e.terminal = terminalInteractive
	e.WatchInterrupts()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	outMaster, outSlave, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	go io.Copy(io.Discard, outMaster) //nolint:errcheck // drain the terminal
	dup, err := syscall.Dup(int(slave.Fd()))
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	stdin := os.NewFile(uintptr(dup), "stdin")
	orig := os.Stdin
	os.Stdin = stdin
	defer func() {
		os.Stdin = orig
		for _, f := range []*os.File{stdin, slave, master, outSlave, outMaster} {
			f.Close()
		}
	}()
	ready := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type outcome struct {
		res *Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := e.Execute(ctx, `for i in 1 2; do sh -c ": > `+ready+`$i; sleep 5"; done; echo after`, outSlave, io.Discard)
		done <- outcome{res, err}
	}()
	waitForPath(t, ready+"1")
	if _, err := master.Write([]byte{0x03}); err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-done:
		if o.err != nil || o.res.ExitCode != 130 || o.res.End != LineInterrupted || strings.Contains(o.res.CapturedOutput, "after") {
			t.Errorf("result %+v, %v; want status 130, not exited, no after", o.res, o.err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("the line went on after its PTY command died of Ctrl+C")
	}
	if _, err := os.Stat(ready + "2"); err == nil {
		t.Error("the loop ran again")
	}
	select {
	case <-sigs:
		t.Error("hash sent itself a SIGINT")
	default:
	}
}
