package shell

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

const (
	oscProgressStart = "\x1b]9;4;3;0\x07"
	oscProgressDone  = "\x1b]9;4;0\x07"
	oscProgressError = "\x1b]9;4;2\x07"
)

// A failed model update shows the red badge briefly. A ?? started within
// that window owns the bar: the pending clear must not wipe its indicator.
func TestProgressBar_FailureClearDoesNotWipeANewerStart(t *testing.T) {
	t.Parallel()
	var out lockedBuffer
	p := newProgressBar(&out)

	p.fail()
	p.start()
	time.Sleep(progressFailHold + 200*time.Millisecond)

	if got, want := out.String(), oscProgressError+oscProgressStart; got != want {
		t.Errorf("wrote %q, want %q (no clear after the newer start)", got, want)
	}
}

func TestProgressBar_FailureClearsItselfWhenUnclaimed(t *testing.T) {
	t.Parallel()
	var out lockedBuffer
	p := newProgressBar(&out)

	p.fail()
	time.Sleep(progressFailHold + 200*time.Millisecond)

	if got, want := out.String(), oscProgressError+oscProgressDone; got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}

func TestProgressBar_NilIsSafe(t *testing.T) {
	var p *progressBar
	p.start()
	p.fail()
	p.done()
}
