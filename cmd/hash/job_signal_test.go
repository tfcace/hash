package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// jobProbe is a background command that says it is ready in rec and,
// unless a signal kills it, says end there a second later. Its INT trap
// records a SIGINT it got.
func jobProbe(rec string) string {
	return `sh -c 'trap "echo got-int >> ` + rec + `" INT; echo ready >> ` + rec + `; i=0; while [ $i -lt 20 ]; do sleep 0.05; i=$((i+1)); done; echo end >> ` + rec + `' &`
}

// waitForFile waits until path holds want, under a deadline.
func waitForFile(t *testing.T, path, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), want) || time.Now().After(deadline) {
			return string(data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The terminal's Ctrl+C misses a background job's command, which leads a
// process group of its own, as a job of bash with job control, and a
// script that dies of it leaves the job running. (hash runs under a
// session leader of its own, as from a person's shell.)
func TestLaunch_InterruptLeavesBackgroundJobsAlone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, fg string
		ended    string
	}{
		{"the script goes on", `sh -c 'trap "echo caught" INT; echo fg-ready; sleep 0.5 & wait'; echo after`, "exit status 0"},
		{"the script dies of it", `echo fg-ready; sleep 5`, "signal: interrupt"},
	} {
		rec := filepath.Join(t.TempDir(), "rec")
		s := startTTYLed(t, "-c", jobProbe(rec)+"\n"+tc.fg)
		waitForFile(t, rec, "ready", 5*time.Second)
		s.waitFor(0, "fg-ready", 5*time.Second)
		s.typeString("\x03")
		if ended := s.hashEnded(5 * time.Second); ended != tc.ended {
			t.Errorf("%s: hash ended %v, want %s", tc.name, ended, tc.ended)
		}
		if got := waitForFile(t, rec, "end", 5*time.Second); got != "ready\nend\n" {
			t.Errorf("%s: the job recorded %q, want ready and end with no SIGINT", tc.name, got)
		}
	}
}

// A background job writes to the terminal as in bash (no tostop).
func TestLaunch_BackgroundJobWritesToTheTerminal(t *testing.T) {
	s := startTTY(t, "-c", `sh -c 'echo job-wrote' & wait; echo after`)
	s.wait(5 * time.Second)
	if out := s.output(); !strings.Contains(out, "job-wrote") || !strings.Contains(out, "after") {
		t.Errorf("terminal %q, want job-wrote, then after", out)
	}
}

// The interactive shell hands its jobs the hangup that ends it, as bash
// does. (Under a leader of its own, so the terminal hangs nothing up.)
func TestInteractive_HangupReachesBackgroundJobs(t *testing.T) {
	t.Parallel()
	rec := filepath.Join(t.TempDir(), "rec")
	s := startTTYLed(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	s.typeString(hupProbe(rec) + "\r")
	waitForFile(t, rec, "ready", 5*time.Second)
	s.signalHash(syscall.SIGHUP)
	s.hashEnded(5 * time.Second)
	if got := waitForFile(t, rec, "hup", 3*time.Second); got != "ready\nhup\n" {
		t.Errorf("the job recorded %q, want ready, then hup", got)
	}
}

// In the interactive shell too, Ctrl+C at a foreground pipeline (which
// keeps the terminal's signals: its tail writes to the terminal itself) leaves
// a background job alone.
func TestInteractive_InterruptLeavesBackgroundJobsAlone(t *testing.T) {
	t.Parallel()
	rec := filepath.Join(t.TempDir(), "rec")
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	s.typeString(jobProbe(rec) + "\r")
	waitForFile(t, rec, "ready", 5*time.Second)
	from := len(s.output())
	s.typeString("sh -c 'echo fg-ready; sleep 3' | cat\r")
	s.waitFor(from, "fg-ready", 5*time.Second)
	s.typeString("\x03")
	if got := waitForFile(t, rec, "end", 5*time.Second); got != "ready\nend\n" {
		t.Errorf("the job recorded %q, want ready and end with no SIGINT", got)
	}
	s.typeString("exit\r")
	s.wait(5 * time.Second)
}

// hupProbe is a background job that records the SIGHUP it gets in rec,
// after saying it is ready there. Its sleeps are short, so its trap runs
// soon after the signal.
func hupProbe(rec string) string {
	return `sh -c 'trap "echo hup >> ` + rec + `; exit" HUP; echo ready >> ` + rec + `; i=0; while [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done' &`
}

// The interactive shell hangs up every job still running when a SIGHUP
// ends it, whatever became of the line that started the job: one Ctrl+C
// ended, one that started it through a function named by a variable. The
// hangup used to go only to the jobs of lines whose context outlived them.
func TestInteractive_HangupReachesTheJobsOfEveryLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	viaVar, interrupted := filepath.Join(dir, "via-var"), filepath.Join(dir, "interrupted")
	s := startTTYLed(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	s.typeString("f() { " + hupProbe(viaVar) + " }\r")
	s.typeString("x=f; $x\r")
	waitForFile(t, viaVar, "ready", 5*time.Second)
	from := len(s.output())
	s.typeString(hupProbe(interrupted) + " sleep 5; echo not-interrupted\r")
	waitForFile(t, interrupted, "ready", 5*time.Second)
	time.Sleep(100 * time.Millisecond) // the line is at its sleep
	s.typeString("\x03")
	s.waitFor(from, "\x1b]133;D;130", 5*time.Second)
	s.signalHash(syscall.SIGHUP)
	s.hashEnded(5 * time.Second)
	for _, rec := range []string{viaVar, interrupted} {
		if got := waitForFile(t, rec, "hup", 3*time.Second); got != "ready\nhup\n" {
			t.Errorf("%s: the job recorded %q, want ready, then hup", filepath.Base(rec), got)
		}
	}
}

// A job's command writes to the terminal itself in the interactive shell,
// as a bash job does: hash used to put a pipe between them (it tees the
// line's output), so the command saw no terminal, and once hash exited
// its next write killed it with SIGPIPE (bash's report of a child the
// hangup killed did that to a job that trapped SIGHUP).
func TestInteractive_BackgroundCommandWritesToTheTerminalItself(t *testing.T) {
	t.Parallel()
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	from := len(s.output())
	s.typeString(`sh -c 'if [ -t 1 ] && [ -t 2 ]; then echo ttys-$((6*7)); else echo pipe-$((6*7)); fi' &` + "\r")
	s.waitFor(from, "-42", 5*time.Second)
	if out := s.output()[from:]; !strings.Contains(out, "ttys-42") {
		t.Errorf("terminal %q, want the job to see the terminal on stdout and stderr", out)
	}
	s.typeString("exit\r")
	s.wait(5 * time.Second)
}

// A script's job's command shares the script's process group, as a job of
// a bash with no job control does: one that sets the terminal up (stty)
// runs to its end, a child of macOS's /bin/sh too, which resets SIGTTOU.
// In a process group of its own that child was stopped by SIGTTOU, with no
// job control to resume it, and the wait for it never ended.
func TestLaunch_BackgroundCommandUsesTheTerminal(t *testing.T) {
	s := startTTY(t, "-c", `sh -c "stty -echo </dev/tty; echo set" & wait; echo done`)
	s.waitFor(0, "done", 5*time.Second)
	s.wait(5 * time.Second)
	if out := s.output(); !strings.Contains(out, "set") {
		t.Errorf("terminal %q, want set, then done", out)
	}
}

// The terminal hanging up reaches a script's job, as it reaches a job of
// bash with no job control: the job's own process group kept it out. (No
// goroutine reads the terminal here, so closing it really hangs it up.)
func TestLaunch_TerminalHangupReachesBackgroundJobs(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	cmd := exec.Command(hashBinary(t), "-c", hupProbe(rec)+"\nsleep 10")
	cmd.Env = isolatedEnv(t.TempDir())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tty.Close()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	kill := time.AfterFunc(15*time.Second, func() { _ = cmd.Process.Kill() })
	defer kill.Stop()
	waitForFile(t, rec, "ready", 5*time.Second)
	ptmx.Close() // the terminal hangs up
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("hash did not end when its terminal hung up")
	}
	if got := waitForFile(t, rec, "hup", 3*time.Second); got != "ready\nhup\n" {
		t.Errorf("the job recorded %q, want ready, then hup", got)
	}
}

// Ctrl+C at a script's wait ends the script at once, as in bash 5.2, and
// leaves the job it waited for running.
func TestLaunch_InterruptEndsAWait(t *testing.T) {
	t.Parallel()
	rec := filepath.Join(t.TempDir(), "rec")
	s := startTTYLed(t, "-c", jobProbe(rec)+"\necho waiting; wait; echo after")
	waitForFile(t, rec, "ready", 5*time.Second)
	s.waitFor(0, "waiting", 5*time.Second)
	time.Sleep(100 * time.Millisecond) // at the wait
	start := time.Now()
	s.typeString("\x03")
	if ended := s.hashEnded(5 * time.Second); ended != "signal: interrupt" {
		t.Errorf("hash ended %v, want signal: interrupt", ended)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("hash took %v to end after Ctrl+C at wait", took)
	}
	if got := waitForFile(t, rec, "end", 5*time.Second); got != "ready\nend\n" {
		t.Errorf("the job recorded %q, want ready and end with no SIGINT", got)
	}
}
