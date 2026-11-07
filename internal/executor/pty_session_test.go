package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runTyping runs line on a PTY terminal (newPTYTerminal), typing typed
// into it after a moment, while the line's command runs.
func runTyping(t *testing.T, line, typed string) *Result {
	t.Helper()
	_, res := runTypingOn(t, line, typed)
	return res
}

// runTypingOn is runTyping, which also returns the executor the line ran on.
func runTypingOn(t *testing.T, line, typed string) (*Executor, *Result) {
	t.Helper()
	term := newPTYTerminal(t)
	e := term.shell()
	time.AfterFunc(300*time.Millisecond, func() { _, _ = term.in.WriteString(typed) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := e.Execute(ctx, line, term.out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return e, res
}

// Keys typed while a command that does not read them runs on a PTY are the
// next prompt's, as in bash, where they stay on the terminal: `sleep 1`,
// then `echo TA2⏎` typed during it, runs echo TA2 next. They were copied
// into the command's PTY and died with it (on macOS the exit of the PTY's
// session leader revokes the terminal, its queued input with it). A line
// typed without its Enter comes back too.
func TestExecute_KeysTypedDuringAPTYCommandComeBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ typed, want string }{
		{"echo TA2\r", "echo TA2\n"}, // the PTY's ICRNL made the Enter a newline
		{"partial", "partial"},
		{"echo TA2\rpartial", "echo TA2\npartial"},
	} {
		e, res := runTypingOn(t, `sleep 0.8`, tc.typed)
		if got := e.TakeUnread(); string(got) != tc.want || res.ExitCode != 0 {
			t.Errorf("typed %q: unread %q, status %d; want %q, 0", tc.typed, got, res.ExitCode, tc.want)
		}
	}
}

// Keys a command reads are its own, not the next prompt's.
func TestExecute_KeysAPTYCommandReadsDoNotComeBack(t *testing.T) {
	t.Parallel()
	e, _ := runTypingOn(t, `head -n 1`, "hello\rnext")
	if got := e.TakeUnread(); string(got) != "next" {
		t.Errorf("unread %q, want only what head left (next)", got)
	}
}

// A command on a PTY runs under hash itself, the PTY's session leader
// (ptyexec), which ends as the command ended: its exit status and its
// death of a signal pass through, also of a signal the Go runtime would
// take as a crash of its own (no traceback), and an exec failure is the
// command's, in bash's words, with a file with no shebang run as a sh
// script.
func TestExecute_PTYSessionLeaderEndsAsTheCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := filepath.Join(dir, "noshebang")
	if err := os.WriteFile(script, []byte("echo from-script\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "garbage")
	if err := os.WriteFile(binary, []byte("\x00\x01\x02garbage\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ line, want string }{
		{`sh -c 'exit 3'; echo rc=$?`, "rc=3"},
		{`sh -c 'kill -HUP $$'; echo rc=$?`, "rc=129"},
		{`sh -c 'kill -SEGV $$'; echo rc=$?`, "rc=139"},
		{`sh -c 'kill -QUIT $$'; echo rc=$?`, "rc=131"},
		{script + `; echo rc=$?`, "from-script\nrc=0"},
		{binary + `; echo rc=$?`, "rc=126"}, // the refusal goes to the line's stderr
	} {
		got := ttyShell(t, tc.line)
		if !strings.Contains(got, tc.want) || strings.Contains(got, "goroutine") {
			t.Errorf("%s: terminal shows %q, want %q", tc.line, got, tc.want)
		}
	}
	var ne *CommandNotExecutableError
	if res := runTyping(t, binary, ""); len(res.Refusals) != 1 || !errors.As(res.Refusals[0], &ne) || res.ExitCode != 126 {
		t.Errorf("refusals %v, status %d; want the binary refused, 126", res.Refusals, res.ExitCode)
	}
}

// With hash's own binary gone (an upgrade removed it) a PTY command leads
// its PTY itself: it runs, and nothing comes back.
func TestExecute_PTYCommandRunsWithoutHashsBinary(t *testing.T) {
	t.Parallel()
	e := New()
	e.shellPath = filepath.Join(t.TempDir(), "gone")
	if got := ttyShellOn(t, e, `sh -c 'echo ran; exit 4'; echo rc=$?`); !strings.Contains(got, "ran\nrc=4") {
		t.Errorf("terminal shows %q, want ran and rc=4", got)
	}
}

// UsedPTY says a command ran on a PTY, not that one drew on the terminal:
// a pipeline's tail writes to the terminal itself (`echo hi | cat`), with
// no PTY. It hid the progress bar as a PTY command does, and was counted
// as one: the shell took its (uncaptured) output for a PTY's merged
// stderr.
func TestExecute_UsedPTYOnlyWhenAPTYRan(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line string
		pty  bool
	}{
		{`echo hi | cat`, false},
		{`cat < /dev/null`, false},
		{`/usr/bin/true`, true},
		{`echo hi`, false}, // a builtin
	} {
		if res := runTyping(t, tc.line, ""); res.UsedPTY != tc.pty {
			t.Errorf("%s: UsedPTY = %v, want %v", tc.line, res.UsedPTY, tc.pty)
		}
	}
}

// An exec at the interactive prompt of a command that runs on a PTY ends
// the shell only once the PTY helper (ptyexec) started the command: one
// that cannot start is refused with 126 and the line goes on, as for a
// command with no PTY (TestExecute_ExecEndsTheShellOnceItsCommandStarted).
func TestExecute_ExecOfAPTYCommandThatCannotStartLeavesTheShell(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "garbage")
	if err := os.WriteFile(binary, []byte("\x00\x01\x02garbage\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ttyShell(t, "exec "+binary+"; echo after=$?"); !strings.Contains(got, "after=126") {
		t.Errorf("terminal shows %q, want the line to go on with after=126", got)
	}
}

// A program at hash's path that is not the hash that runs (an older or a
// newer build put there by an upgrade or a downgrade, speaking another
// report or none) does not lead a PTY command's PTY: the command runs as
// the leader itself, as a job's does without jobexec (selfBinary). It ran
// the foreign program instead, which said nothing, so every PTY command
// failed, and an exec of one ended the shell.
func TestExecute_PTYCommandRunsDespiteAForeignProgramAtHashsPath(t *testing.T) {
	t.Parallel()
	other := filepath.Join(t.TempDir(), "hash")
	if err := os.WriteFile(other, []byte("#!/bin/sh\necho \"hash: $1: no such file or directory\" >&2\nexit 127\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`sh -c 'echo ran; exit 4'; echo rc=$?`,
		`exec sh -c 'echo ran; exit 4'; echo rc=$?`, // the shell ends with the command: no rc
	} {
		e := New()
		e.shellPath = other
		got := ttyShellOn(t, e, line)
		if !strings.Contains(got, "ran") || strings.Contains(got, "no such file") {
			t.Errorf("%s: terminal shows %q, want the command to run", line, got)
		}
	}
}

// fakeLeader makes a script, taken for hash's own binary, the session
// leader of e's PTY commands: it runs body (sh) with the report pipe on fd
// 3, as a hash of another build would, or one killed before its report.
func fakeLeader(t *testing.T, e *Executor, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hash")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.shellPath = path
	e.self = binaryAt(path)
}

// A leader that ends without a word (killed before it reported) started
// nothing hash knows of: the status is the leader's, and an exec of the
// command leaves the shell as one that could not start does. It was taken
// for a start, and the exec ended the shell.
func TestExecute_APTYLeaderThatSaysNothingStartedNothing(t *testing.T) {
	t.Parallel()
	e := New()
	fakeLeader(t, e, "exit 127")
	if got := ttyShellOn(t, e, `exec sh -c 'echo ran'; echo after=$?`); !strings.Contains(got, "after=127") {
		t.Errorf("terminal shows %q, want the line to go on with after=127", got)
	}
}

// A leader that says a word out of turn (the start twice) is no panic: the
// word is dropped.
func TestExecute_APTYLeaderThatRepeatsItselfIsNoPanic(t *testing.T) {
	t.Parallel()
	e := New()
	fakeLeader(t, e, "printf ss >&3; printf eu >&3; exit 3")
	if got := ttyShellOn(t, e, `sh -c 'echo ran'; echo rc=$?`); !strings.Contains(got, "rc=3") {
		t.Errorf("terminal shows %q, want the leader's status, rc=3", got)
	}
}

// A paste bigger than a PTY's input queue, typed while a command that does
// not read it runs, comes back whole and in order once the command ended,
// and does not hold up its end. The copy blocked writing it to the full
// PTY, so the leader waited out its release (2 s) and the keys the copy
// held, half the paste, were lost.
func TestExecute_APasteBiggerThanThePTYsQueueComesBack(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0") // the leader, this binary, exits at once under -race
	term := newPTYTerminal(t)
	e := term.shell()
	var paste strings.Builder
	for i := range 400 {
		fmt.Fprintf(&paste, "echo L%03d\r", i)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_, _ = term.in.WriteString(paste.String())
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	_, err := e.Execute(ctx, `sleep 1`, term.out, io.Discard)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	unread := e.TakeUnread()
	// What the copy never read is still on the terminal, the next
	// prompt's after the unread keys.
	var left []byte
	buf := make([]byte, 4096)
	for hasDataAvailable(int(term.stdin.Fd()), 300*time.Millisecond) {
		n, err := term.stdin.Read(buf)
		if err != nil || n == 0 {
			break
		}
		left = append(left, buf[:n]...)
	}
	got := strings.ReplaceAll(string(unread)+string(left), "\r", "\n")
	want := strings.ReplaceAll(paste.String(), "\r", "\n")
	if got != want || took > 1800*time.Millisecond {
		t.Errorf("sleep 1 took %v; %d of the %d keys came back (Unread %d, left %d), in order: %v",
			took.Round(time.Millisecond), len(got), len(want), len(unread), len(left), got == want)
	}
}

// A background child a PTY command leaves running outlives it, as in bash:
// `sh -c 'sleep 5 >/dev/null 2>&1 </dev/null &'`. It ran in the leader's
// process group, the PTY's foreground, which the hangup at the leader's
// exit reached. Nor does it hold up the command's end, though it holds the
// PTY, or a stderr hash copies.
func TestExecute_APTYCommandsBackgroundChildOutlivesIt(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0") // the leader, this binary, exits at once under -race
	term := newPTYTerminal(t)
	e := term.shell()
	pidFile := filepath.Join(t.TempDir(), "pid")
	for _, tc := range []struct {
		redirects string
		stderr    io.Writer
	}{
		{" >/dev/null 2>&1 </dev/null", term.out},
		{"", term.out},
		{"", io.Discard}, // a stderr hash copies, which the child holds
	} {
		redirects := tc.redirects
		start := time.Now()
		_, err := e.Execute(context.Background(), `sh -c 'sleep 5`+redirects+` & echo $! > `+pidFile+`'`, term.out, tc.stderr)
		took := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		b, _ := os.ReadFile(pidFile)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if pid <= 0 {
			t.Fatalf("no pid in %q", b)
		}
		if err := syscall.Kill(pid, 0); err != nil {
			t.Errorf("%q: the background child %d of a PTY command is gone: %v", redirects, pid, err)
		} else {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if took > time.Second {
			t.Errorf("%q: the command took %v to end, want the child not to hold it up", redirects, took)
		}
	}
}

// A PTY command that ignores the signal of its canceled line is killed a
// grace later, its process group with it, though the command's group is
// not the leader's (the leader cannot pass SIGKILL on).
func TestExecute_ACanceledPTYCommandThatIgnoresItsSignalIsKilled(t *testing.T) {
	defer func(g time.Duration) { commandStopGrace = g }(commandStopGrace)
	commandStopGrace = 300 * time.Millisecond
	term := newPTYTerminal(t)
	e := term.shell()
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	start := time.Now()
	_, _ = e.Execute(ctx, `sh -c 'trap "" TERM HUP INT; sleep 30 & echo $! > `+pidFile+`; wait'`, term.out, term.out)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("the canceled line took %v to end", took)
	}
	b, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid <= 0 {
		t.Fatalf("no pid in %q", b)
	}
	time.Sleep(100 * time.Millisecond)
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("the command's child %d lives on", pid)
	}
}

// HASH_PTY_TRACE writes its trace to the temporary directory, not the
// working directory (a repository, a shared folder), and says how many
// keys came, not which: it logged the keys typed ahead of a command, a
// password among them, to hash-pty-trace.log wherever the command ran.
func TestPTYTrace_LogsNoKeysAndNotInTheWorkingDirectory(t *testing.T) {
	cwd, tmp := t.TempDir(), t.TempDir()
	t.Chdir(cwd)
	t.Setenv("TMPDIR", tmp)
	t.Setenv("HASH_PTY_TRACE", "1")
	t.Setenv("HASH_PTY_TRACE_PATH", "")
	term := newPTYTerminal(t)
	e := term.shell()
	if _, err := term.in.WriteString("hunter2\r"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := e.Execute(context.Background(), `head -n 1`, term.out, term.out); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Errorf("the trace went to the working directory: %v", entries)
	}
	traces, _ := filepath.Glob(filepath.Join(tmp, "*"))
	if len(traces) == 0 {
		t.Fatal("no trace in the temporary directory")
	}
	for _, path := range traces {
		if b, _ := os.ReadFile(path); strings.Contains(string(b), "hunter2") {
			t.Errorf("%s logs the keys typed", path)
		}
	}
}

// Ctrl+Z (SIGTSTP) does not stop a PTY command for good: hash has no job
// control for it, and its leader waited on the stopped command, holding
// the shell. While the command shared the leader's process group, an
// orphaned one, the terminal's SIGTSTP was discarded; in a group of its
// own it stopped. The leader now continues it.
func TestExecute_APTYCommandThatStopsGoesOn(t *testing.T) {
	commandStopGrace = 300 * time.Millisecond
	defer func(g time.Duration) { commandStopGrace = g }(2 * time.Second)
	term := newPTYTerminal(t)
	e := term.shell()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	res, err := e.Execute(ctx, `sh -c 'kill -TSTP $$; echo resumed'`, term.out, term.out)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); !strings.Contains(res.CapturedOutput, "resumed") || took > 3*time.Second {
		t.Errorf("took %v, output %q; want the command to go on and say resumed", took.Round(time.Millisecond), res.CapturedOutput)
	}
}

// A PTY command's whole output reaches a terminal that reads it slowly,
// though the command ended long before the terminal took the last of it:
// `seq 1 20000` lost its last lines on Linux.
func TestExecute_APTYCommandsOutputReachesASlowTerminal(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	got := make(chan []byte, 1)
	term := newPTYTerminalReadBy(t, func(outMaster *os.File) {
		var all []byte
		buf := make([]byte, 1024)
		for !bytes.Contains(all, []byte("END\r\n")) { // 1 KB every 20 ms
			n, err := outMaster.Read(buf)
			all = append(all, buf[:n]...)
			if err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		got <- all
	})
	e := term.shell()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := e.Execute(ctx, `seq 1 20000`, term.out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.Execute(ctx, `echo END`, term.out, io.Discard)
	out := strings.ReplaceAll(string(<-got), "\r", "")
	if !res.UsedPTY || !strings.Contains(out, "\n20000\n") {
		i := strings.Index(out, "END")
		t.Errorf("PTY %t; the output's tail is lost: %d bytes, the last before END: %q", res.UsedPTY, len(out), out[max(0, i-60):max(0, i)])
	}
}

// A double Ctrl+C that ends a program with its terminal raw (the keys
// reach it as bytes, no signal) leaves no Ctrl+C for the next prompt: the
// two it never read came back as typeahead and cleared two prompts. A
// program that handles the SIGINT and exits 130 ended of it as well.
func TestExecute_ADoubleCtrlCThatEndsARawProgramLeavesNoCtrlC(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		`sh -c 'stty raw -echo; sleep 5'`,
		`sh -c 'trap "exit 130" INT; stty raw -echo; sleep 5 & wait'`,
	} {
		e, res := runTypingOn(t, line, "ab\x03\x03")
		if got := e.TakeUnread(); string(got) != "ab" || res.ExitCode != 130 {
			t.Errorf("%s: unread %q, status %d; want ab, 130", line, got, res.ExitCode)
		}
	}
}

// Ctrl+C presses: a second within doubleCtrlCWait of the first sends the
// SIGINT, which ended the command when it died of it or exited 130; one
// press alone, or two far apart, sent none.
func TestCtrlCPresses(t *testing.T) {
	now := time.Now()
	var p ctrlCPresses
	if p.press(now) || p.press(now.Add(2*doubleCtrlCWait)) {
		t.Fatal("a single press, or one long after the last, sent SIGINT")
	}
	if p.endedIt(exitedWith(t, 130)) {
		t.Error("an exit of 130 with no SIGINT sent counts as its end")
	}
	if !p.press(now.Add(2*doubleCtrlCWait + doubleCtrlCWait/2)) {
		t.Fatal("a second press soon after the first sent no SIGINT")
	}
	if !p.endedIt(exitedWith(t, 130)) || p.endedIt(exitedWith(t, 1)) {
		t.Error("the SIGINT sent ended a command that exited 130, and only that")
	}
}

// exitedWith is a command that ran and exited with status.
func exitedWith(t *testing.T, status int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit "+strconv.Itoa(status))
	_ = cmd.Run()
	return cmd
}
