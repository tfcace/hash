package shell

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/executor"
)

// jobPid starts line as a job at the prompt, its command writing its pid
// to a file first, and returns the pid.
func jobPid(t *testing.T, sh *Shell, line string) int {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	sh.executor.SetExportedEnv("PIDFILE", pidFile)
	if _, err := sh.executeLine(context.Background(), line, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	var pid int
	waitFor(t, func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil || !strings.HasSuffix(string(b), "\n") {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil
	})
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

// gone waits a moment for pid to end (and its job to reap it).
func gone(pid int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// At the shell's fatal end every job gets SIGHUP, whatever the signal, as
// bash resends only SIGHUP: a plain job dies of it, and one that ignores
// it (nohup) runs on, a SIGTERM to hash included. The jobs got the fatal
// signal itself, so SIGTERM killed the nohup job.
func TestCloseOnSignal_HangsUpTheJobs(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			sh := &Shell{executor: executor.New()}
			plain := jobPid(t, sh, `sh -c 'echo $$ >"$PIDFILE"; exec sleep 30' &`)
			nohup := jobPid(t, sh, `nohup sh -c 'echo $$ >"$PIDFILE"; exec sleep 30' >/dev/null 2>&1 &`)
			_ = sh.CloseOnSignal(sig)
			if !gone(plain) {
				t.Error("a job runs on after the shell's fatal end")
			}
			if err := syscall.Kill(nohup, 0); err != nil {
				t.Errorf("the nohup job died at the shell's fatal end: %v", err)
			}
		})
	}
}
