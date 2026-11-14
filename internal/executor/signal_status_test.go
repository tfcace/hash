package executor

import (
	"bytes"
	"context"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A command killed by a signal sets 128+signal, as in bash: Ctrl+C on
// `sleep 5` leaves $? at 130, not 1.
func TestExecute_SignaledCommandSetsSignalStatus(t *testing.T) {
	tests := []struct{ line, want string }{
		{`sh -c 'kill -INT $$'; echo $?`, "130"},
		{`sh -c 'kill -TERM $$'; echo $?`, "143"},
		{`sh -c 'kill -INT $$' | cat; echo $?`, "0"},
		{`true | sh -c 'kill -INT $$'; echo $?`, "130"},
	}
	for _, tt := range tests {
		e := New()
		var out bytes.Buffer
		if _, err := e.Execute(context.Background(), tt.line, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out.String()); got != tt.want {
			t.Errorf("%s: $? = %q, want %s", tt.line, got, tt.want)
		}
	}
	e := New()
	res, err := e.Execute(context.Background(), `sh -c 'kill -INT $$'`, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 130 {
		t.Errorf("ExitCode = %d, want 130", res.ExitCode)
	}
}

// The PTY path derives the status the same way. (A PTY command's death
// of SIGINT ends the line instead, as Ctrl+C: see
// TestExecute_PTYCommandDyingOfCtrlCEndsTheLineWithNoSignal.)
func TestExecute_SignaledPTYCommandSetsSignalStatus(t *testing.T) {
	t.Parallel()
	got := ttyShell(t, `sh -c 'kill -TERM $$'; echo "rc=$?"`)
	if !strings.Contains(got, "rc=143") {
		t.Fatalf("PTY output %q, want rc=143", got)
	}
}

// testSignalCause is a context cause that names a signal, as hash's fatal
// signals do.
type testSignalCause struct{ sig syscall.Signal }

func (c testSignalCause) Error() string          { return c.sig.String() }
func (c testSignalCause) Signal() syscall.Signal { return c.sig }

// A command running when its line is canceled gets the signal that ended
// hash (the cause), as it would from the terminal, so it can clean up; one
// that ignores it is killed after a grace. It used to be killed at once.
func TestExecute_CanceledCommandGetsTheSignalFirst(t *testing.T) {
	old := commandStopGrace
	commandStopGrace = 300 * time.Millisecond
	defer func() { commandStopGrace = old }()
	run := func(line string) (string, time.Duration) {
		ctx, cancel := context.WithCancelCause(context.Background())
		time.AfterFunc(300*time.Millisecond, func() { cancel(testSignalCause{syscall.SIGHUP}) })
		var out bytes.Buffer
		start := time.Now()
		if _, err := New().Execute(ctx, line, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		return out.String(), time.Since(start)
	}
	if got, took := run(`sh -c 'trap "echo got-hup; exit 3" HUP; while :; do sleep 0.05; done'`); got != "got-hup\n" || took > 3*time.Second {
		t.Errorf("command trapping HUP printed %q after %v, want got-hup", got, took)
	}
	if got, took := run(`sh -c 'trap "" HUP; while :; do sleep 0.05; done; echo never'`); got != "" || took > 3*time.Second {
		t.Errorf("command ignoring HUP: printed %q, ended after %v; want it killed after the grace", got, took)
	}
}
