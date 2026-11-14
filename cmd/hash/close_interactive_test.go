package main

import (
	"context"
	"fmt"
	"syscall"
	"testing"
)

// closeRecorder is an interactive shell that records how it was closed.
type closeRecorder struct{ closed []string }

func (r *closeRecorder) CloseOnSignal(sig syscall.Signal) error {
	r.closed = append(r.closed, fmt.Sprintf("CloseOnSignal(%v)", sig))
	return nil
}

func (r *closeRecorder) Close() error {
	r.closed = append(r.closed, "Close")
	return nil
}

func (r *closeRecorder) ExitStatus() int { return 3 }

// hangsUpAfterFirstRead is hash's context with a SIGHUP that arrives just
// after the first reading of it: the terminal closing as the shell exits.
type hangsUpAfterFirstRead struct {
	context.Context
	reads int
}

func (c *hangsUpAfterFirstRead) Err() error {
	c.reads++
	if c.reads > 1 {
		return signalError{syscall.SIGHUP}
	}
	return nil
}

// The interactive shell closes on the one reading of the fatal signal that
// endOf made. After `exit 3`, a SIGHUP that came between endOf's reading
// and a second one in the closer made the closer take 3 for 128+signal
// and hang the jobs up with signal -125; it closes as endOf saw the end,
// and hash then dies of the late SIGHUP.
func TestInteractiveCloser_ClosesOnEndOfsReadingOfTheSignal(t *testing.T) {
	sh := &closeRecorder{}
	ctx := &hangsUpAfterFirstRead{Context: context.Background()}
	got := endOf(ctx, 3, interactiveCloser(sh, nil))
	if fmt.Sprint(sh.closed) != "[Close]" {
		t.Errorf("closed with %v after exit 3, want [Close]: endOf saw no signal", sh.closed)
	}
	if want := (shellEnd{status: 129, signal: syscall.SIGHUP}); got != want {
		t.Errorf("endOf = %+v, want %+v (the SIGHUP came while the shell closed)", got, want)
	}

	hungUp, hangUp := context.WithCancelCause(context.Background())
	hangUp(signalError{syscall.SIGHUP})
	sh = &closeRecorder{}
	endOf(hungUp, 0, interactiveCloser(sh, nil))
	if fmt.Sprint(sh.closed) != "[CloseOnSignal(hangup)]" {
		t.Errorf("closed with %v after SIGHUP, want [CloseOnSignal(hangup)]", sh.closed)
	}
}
