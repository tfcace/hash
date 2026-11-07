package executor

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/progress"
)

const (
	progressOn  = "\x1b]9;4;3;0\x07"
	progressOff = "\x1b]9;4;0\x07"
)

// progressRecorder records OSC 9;4 output; the first write can be slowed to
// widen the window between deciding to show progress and recording it.
type progressRecorder struct {
	mu        sync.Mutex
	out       strings.Builder
	firstSlow time.Duration
	lastOff   time.Time // when progress was last removed
}

func (r *progressRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	slow := r.firstSlow
	r.firstSlow = 0
	r.mu.Unlock()
	time.Sleep(slow)
	r.mu.Lock()
	defer r.mu.Unlock()
	if string(p) == progressOff {
		r.lastOff = time.Now()
	}
	return r.out.Write(p)
}

func (r *progressRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.out.String()
}

func newProgressExecutor(threshold time.Duration, rec *progressRecorder) *Executor {
	e := New()
	e.progressThreshold = threshold
	e.progressOSC = progress.NewOSC(rec)
	return e
}

// The progress bar is shown iff the sequence ends with it on.
func progressLeftOn(out string) bool {
	on, off := strings.LastIndex(out, progressOn), strings.LastIndex(out, progressOff)
	return on > off
}

// A line that ends while the threshold timer is still showing the progress
// bar must still remove it: the timer and the line's end used to race on a
// flag, leaving the bar stuck after the prompt returned.
func TestExecute_ProgressRemovedWhenLineEndsWhileShowing(t *testing.T) {
	t.Parallel()
	rec := &progressRecorder{firstSlow: 200 * time.Millisecond}
	e := newProgressExecutor(10*time.Millisecond, rec)
	if _, err := e.Execute(context.Background(), "sleep 0.05", io.Discard, io.Discard); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // let a late Start land
	if out := rec.String(); progressLeftOn(out) || !strings.Contains(out, progressOn) {
		t.Fatalf("progress output %q: want it shown and then removed", out)
	}
}

// A PTY child only hides progress while it runs: a long non-PTY command
// after it in the same line still gets the bar.
func TestExecute_ProgressShowsAfterPTYChildEnds(t *testing.T) {
	t.Parallel()
	rec := &progressRecorder{}
	e := newProgressExecutor(50*time.Millisecond, rec)
	ttyShellOn(t, e, "/usr/bin/true; sleep 0.3 > /dev/null")
	if out := rec.String(); !strings.Contains(out, progressOn) || progressLeftOn(out) {
		t.Fatalf("progress output %q: want the bar for the sleep, removed at the end", out)
	}
}

// A PTY child (a TUI) that starts after the bar is up takes it down for as
// long as it runs.
func TestExecute_PTYChildHidesProgress(t *testing.T) {
	t.Parallel()
	rec := &progressRecorder{}
	e := newProgressExecutor(50*time.Millisecond, rec)
	ttyShellOn(t, e, "sleep 0.2 > /dev/null; sh -c 'sleep 0.3'")
	ended := time.Now()
	out := rec.String()
	// on (sleep), off (TUI starts), and nothing more: the TUI ran 0.3s with
	// no bar and the line ended right after it.
	if out != progressOn+progressOff {
		t.Fatalf("progress output %q, want %q", out, progressOn+progressOff)
	}
	rec.mu.Lock()
	hiddenFor := ended.Sub(rec.lastOff)
	rec.mu.Unlock()
	if hiddenFor < 200*time.Millisecond {
		t.Fatalf("bar removed %v before the line ended, want it removed when the PTY child started", hiddenFor)
	}
}
