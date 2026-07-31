package completion

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

const isolatedCommandWaitDelay = 50 * time.Millisecond

// ConfigureIsolatedCommand puts cmd in its own session with a WaitDelay and
// kills its whole process group on cancellation, so a descendant that keeps
// stdout open cannot block Wait past the context deadline. Exported for
// other packages that run short-lived helper commands (e.g. tool --help
// collection) and need the same containment as completion sources.
func ConfigureIsolatedCommand(cmd *exec.Cmd) {
	configureIsolatedCompletionCommand(cmd)
}

func configureIsolatedCompletionCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.WaitDelay = isolatedCommandWaitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
}
