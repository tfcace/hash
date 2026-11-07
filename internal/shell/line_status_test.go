package shell

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/executor"
)

// signalCause is a shell context's cause once a fatal signal ended it, as
// cmd/hash's is: it names the signal.
type signalCause syscall.Signal

func (s signalCause) Error() string          { return syscall.Signal(s).String() }
func (s signalCause) Signal() syscall.Signal { return syscall.Signal(s) }

// A line the shell's ending cuts off ends with 128 plus the shell's signal,
// as a ?? line does (lineExitCode), whatever the line ran: a loop of
// builtins, a read, an external command. It ended 1, the interpreter's
// status for a context it saw end.
func TestExecuteLine_ShellEndCutsOffALineWithTheSignalsStatus(t *testing.T) {
	t.Parallel()
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(fifo, os.O_RDWR, 0) // a writer that never writes: read waits
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, line := range []string{"while :; do :; done", `read x <"$FIFO"`, "sleep 5"} {
		sh := &Shell{executor: executor.New()}
		sh.executor.SetExportedEnv("FIFO", fifo)
		shell, end := context.WithCancelCause(context.Background())
		time.AfterFunc(300*time.Millisecond, func() { end(signalCause(syscall.SIGHUP)) })
		res, err := sh.executeLine(shell, line, io.Discard, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if want := 128 + int(syscall.SIGHUP); res.ExitCode != want {
			t.Errorf("%q cut off by the shell's SIGHUP ended %d, want %d", line, res.ExitCode, want)
		}
	}
}
