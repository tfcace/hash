package ptyexec

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The session leader's report says the command started, that it ended,
// then what it left unread; or it is its failed exec as os.StartProcess
// reports one. A report cut short (a leader killed before its last word)
// leaves nothing, and says so.
func TestReport(t *testing.T) {
	var starts, ends int
	started := func() { starts++ }
	ended := func() { ends++ }
	left, err := Report(strings.NewReader("seuecho hi\npart"), "/bin/x", started, ended)
	if string(left) != "echo hi\npart" || err != nil || starts != 1 || ends != 1 {
		t.Errorf("started, ended, unread report: %q, %v, %d starts, %d ends", left, err, starts, ends)
	}
	left, err = Report(strings.NewReader("s"), "/bin/x", started, ended)
	if left != nil || !errors.Is(err, ErrCutShort) || starts != 2 || ends != 1 {
		t.Errorf("started, then killed: %q, %v, %d starts, %d ends", left, err, starts, ends)
	}
	left, err = Report(strings.NewReader(""), "/bin/x", started, ended)
	if left != nil || !errors.Is(err, ErrCutShort) || starts != 2 || ends != 1 {
		t.Errorf("empty report: %q, %v, %d starts, %d ends", left, err, starts, ends)
	}
	_, err = Report(strings.NewReader("x8"), "/bin/x", started, ended)
	var pe *os.PathError
	if !errors.As(err, &pe) || pe.Op != "fork/exec" || pe.Path != "/bin/x" || !errors.Is(err, syscall.ENOEXEC) || starts != 2 {
		t.Errorf("start failure report: %v, %d starts", err, starts)
	}
	if _, err = Report(strings.NewReader("?"), "/bin/x", started, ended); err == nil {
		t.Error("a bad report is no error")
	}
}

// The leader reads the keys left on the PTY only once hash released it:
// keys that reach the PTY after the command ended (hash still copying the
// terminal's keys to it) are reported with the rest, not lost with the PTY.
// Before the release (hash has stopped its copy) nothing is read.
func TestLeaderReadsTheKeysLeftOnceReleased(t *testing.T) {
	path, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true")
	}
	report, reportW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	releaseR, release, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	args := Args(os.Args[0], path, []string{"true"})
	cmd := &exec.Cmd{Path: args[0], Args: args, ExtraFiles: []*os.File{reportW, releaseR}}
	ptmx, err := pty.StartWithAttrs(cmd, nil, &syscall.SysProcAttr{Setsid: true, Setctty: true})
	_ = reportW.Close()
	_ = releaseR.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	go func() { _, _ = io.Copy(io.Discard, ptmx) }() // its echo
	ended := make(chan struct{})
	result := make(chan []byte, 1)
	go func() {
		left, _ := Report(report, path, func() {}, func() { close(ended) })
		result <- left
	}()
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the leader never said the command ended")
	}
	time.Sleep(300 * time.Millisecond) // a while after the end: a slow hash
	if _, err := ptmx.Write([]byte("late keys")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // on the PTY's queue
	_ = release.Close()
	select {
	case left := <-result:
		if string(left) != "late keys" {
			t.Errorf("left unread = %q, want the keys written before the release", left)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no report after the release")
	}
	_ = cmd.Wait()
}

// A hangup of the PTY (hash died, its master closed) ends the command:
// the leader passes it on to the command's group. On Linux the hangup
// reaches the session leader alone, which took it for one the command
// had already, and the command ran on, the leader waiting for it.
func TestLeaderPassesTheHangupOn(t *testing.T) {
	path, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	report, reportW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer report.Close()
	releaseR, release, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()
	args := Args(os.Args[0], path, []string{"sleep", "30"})
	cmd := &exec.Cmd{Path: args[0], Args: args, ExtraFiles: []*os.File{reportW, releaseR}}
	ptmx, err := pty.StartWithAttrs(cmd, nil, &syscall.SysProcAttr{Setsid: true, Setctty: true})
	_ = reportW.Close()
	_ = releaseR.Close()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	go func() { _, _ = Report(report, path, func() { close(started) }, func() {}) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the leader never said the command started")
	}
	time.Sleep(100 * time.Millisecond) // sleep runs
	_ = ptmx.Close()
	_ = release.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
		t.Fatal("the PTY hung up and the command, and its leader, ran on")
	}
}

// Args puts the token between hash and the command.
func TestArgs(t *testing.T) {
	if got := strings.Join(Args("/h", "/bin/ls", []string{"ls", "-l"}), " "); got != "/h "+token+" /bin/ls ls -l" {
		t.Errorf("Args = %q", got)
	}
}
