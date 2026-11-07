package main

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// ttySession is hash running on a terminal (a pty) the test types into and
// reads back, as a person at a terminal would. It answers the terminal
// queries an interactive hash sends (cursor position, device attributes,
// status).
// Everything runs under a hard kill deadline.
type ttySession struct {
	t    *testing.T
	cmd  *exec.Cmd
	ptmx *os.File
	done chan struct{} // closed when hash (or its leader, see startTTYLed) ended
	// status is where hash's leader writes how hash ended; "" when hash
	// leads its session itself.
	status string

	mu  sync.Mutex
	out bytes.Buffer
}

// startTTY starts hash with args on a new terminal, in a home that skips
// the first-run welcome. hash leads its own session with the terminal as
// its controlling one, so the terminal's Ctrl+C reaches it (and its
// foreground children) as SIGINT.
func startTTY(t *testing.T, args ...string) *ttySession {
	t.Helper()
	return startTTYWith(t, nil, args...)
}

// leaderStatusEnv names the file the test binary, run as a session's
// leader (startTTYLed), writes how hash ended to.
const leaderStatusEnv = "HASH_TEST_LEADER_STATUS"

// leadSession is the test binary as the leader of a terminal's session:
// it runs argv (hash) in the foreground, writes how it ended to status,
// and keeps the session until it is killed, as a person's shell runs hash.
// The terminal's Ctrl+C and Ctrl+\ reach the leader too, which drops them
// (handled, so hash does not inherit an ignore).
func leadSession(status string, argv []string) {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGINT, syscall.SIGQUIT)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		_ = os.WriteFile(status, []byte(err.Error()), 0o600)
		os.Exit(1)
	}
	_ = os.WriteFile(status+".pid", []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	_ = cmd.Wait()
	_ = os.WriteFile(status, []byte(cmd.ProcessState.String()), 0o600)
	select {}
}

// startTTYLed is startTTY with hash under a session leader of its own
// (leadSession); hashEnded says how hash ended.
func startTTYLed(t *testing.T, args ...string) *ttySession {
	t.Helper()
	status := filepath.Join(t.TempDir(), "status")
	s := startTTYOn(t, []string{leaderStatusEnv + "=" + status}, os.Args[0], append([]string{hashBinary(t)}, args...)...)
	s.status = status
	return s
}

// hashEnded waits for hash, under a leader (startTTYLed), to end and says
// how it did (as os.ProcessState.String).
func (s *ttySession) hashEnded(timeout time.Duration) string {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(s.status); err == nil && len(data) > 0 {
			return string(data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("hash did not end within %v\nterminal: %q", timeout, s.output())
	return ""
}

// signalHash sends sig to hash (not to its leader, under startTTYLed).
func (s *ttySession) signalHash(sig syscall.Signal) {
	s.t.Helper()
	if s.status == "" {
		_ = s.cmd.Process.Signal(sig)
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(s.status + ".pid"); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil {
				_ = syscall.Kill(pid, sig)
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatal("hash's leader never said hash's pid")
}

// startTTYWith is startTTY with env added to hash's environment (a later
// entry wins, so TERM=... replaces the default dumb terminal).
func startTTYWith(t *testing.T, env []string, args ...string) *ttySession {
	t.Helper()
	return startTTYOn(t, env, hashBinary(t), args...)
}

// startTTYOn runs prog with args on a new terminal, as startTTYWith.
func startTTYOn(t *testing.T, env []string, prog string, args ...string) *ttySession {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	cmd := exec.Command(prog, args...)
	cmd.Env = append(append(isolatedEnv(interactiveHome(t)), "TERM=dumb"), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tty.Close()
	s := &ttySession{t: t, cmd: cmd, ptmx: ptmx, done: make(chan struct{})}
	go s.copyOut()
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	kill := time.AfterFunc(20*time.Second, func() { _ = cmd.Process.Kill() })
	t.Cleanup(func() {
		kill.Stop()
		_ = cmd.Process.Kill()
		<-s.done
		ptmx.Close()
	})
	return s
}

func (s *ttySession) copyOut() {
	buf := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			s.mu.Lock()
			s.out.Write(chunk)
			s.mu.Unlock()
			if bytes.Contains(chunk, []byte("\x1b[6n")) {
				_, _ = s.ptmx.WriteString("\x1b[1;1R")
			}
			if bytes.Contains(chunk, []byte("\x1b[c")) {
				_, _ = s.ptmx.WriteString("\x1b[?62c")
			}
			if bytes.Contains(chunk, []byte("\x1b[5n")) {
				_, _ = s.ptmx.WriteString("\x1b[0n")
			}
		}
		if err != nil {
			return
		}
	}
}

// output is everything hash wrote to the terminal so far.
func (s *ttySession) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// waitFor waits until the terminal shows want after the first from bytes
// of output, and fails the test if it does not within timeout.
func (s *ttySession) waitFor(from int, want string, timeout time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if out := s.output(); len(out) >= from && strings.Contains(out[from:], want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("terminal did not show %q within %v\nterminal: %q", want, timeout, s.output())
}

// answerSettled is how long a test waits, once a question is shown,
// before it types the answer: a question takes only a key that comes once
// the keys settled (the shell's settleGap, 300ms), and drops one typed
// sooner.
const answerSettled = 400 * time.Millisecond

// answer types text as the answer to the question just shown, once the
// keys settled (answerSettled).
func (s *ttySession) answer(text string) {
	time.Sleep(answerSettled)
	s.typeString(text)
}

// typeString types text on the terminal.
func (s *ttySession) typeString(text string) {
	if _, err := s.ptmx.WriteString(text); err != nil {
		s.t.Fatalf("type %q: %v", text, err)
	}
}

// wait waits for hash to end and returns how it ended.
func (s *ttySession) wait(timeout time.Duration) *os.ProcessState {
	s.t.Helper()
	select {
	case <-s.done:
		return s.cmd.ProcessState
	case <-time.After(timeout):
		s.t.Fatalf("hash did not end within %v\nterminal: %q", timeout, s.output())
		return nil
	}
}
