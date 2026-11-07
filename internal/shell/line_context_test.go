package shell

import (
	"context"
	"errors"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/executor"
)

var errShellEnded = errors.New("shell ended")

// ended waits a moment for l to end and reports its cause, nil when it did
// not end.
func ended(l *lineContext) error {
	select {
	case <-l.Done():
		return context.Cause(l)
	case <-time.After(2 * time.Second):
		return nil
	}
}

// A line's context runs while its line does, is kept while a job of the
// line's runs, and ends once the line returned with no job, its last job
// ended, Ctrl+C or the shell ending, whichever comes first. Ended is final.
func TestLineContext_Lifetime(t *testing.T) {
	tests := []struct {
		name  string
		drive func(l *lineContext, endShell func(), jobsDone chan struct{})
		want  error
	}{
		{"returned with no job", func(l *lineContext, _ func(), _ chan struct{}) {
			l.returned(nil)
		}, errLineOver},
		{"its last job ended", func(l *lineContext, _ func(), jobsDone chan struct{}) {
			l.returned(jobsDone)
			if l.Err() != nil {
				t.Error("a line with a job running ended when it returned")
			}
			close(jobsDone)
		}, errLineOver},
		{"the shell ended while a job runs", func(l *lineContext, endShell func(), jobsDone chan struct{}) {
			l.returned(jobsDone)
			endShell()
		}, errShellEnded},
		{"the shell ended while the line runs", func(l *lineContext, endShell func(), jobsDone chan struct{}) {
			endShell()
			ended(l) // the shell's AfterFunc ends it on its own goroutine
			l.returned(jobsDone)
			close(jobsDone)
		}, errShellEnded},
		{"Ctrl+C while the line runs", func(l *lineContext, endShell func(), jobsDone chan struct{}) {
			l.interrupt()
			l.returned(jobsDone) // cut off with it: not kept
			endShell()
		}, errLineInterrupted},
		{"the shell ended after the line", func(l *lineContext, endShell func(), _ chan struct{}) {
			l.returned(nil)
			endShell()
		}, errLineOver},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shell, endShell := context.WithCancelCause(context.Background())
			defer endShell(nil)
			l := newLineContext(shell)
			if l.Err() != nil {
				t.Fatal("a new line is not running")
			}
			tt.drive(l, func() { endShell(errShellEnded) }, make(chan struct{}))
			got := ended(l)
			if !errors.Is(got, tt.want) {
				t.Errorf("line ended with %v, want %v", got, tt.want)
			}
			if l.interrupted() != errors.Is(tt.want, errLineInterrupted) {
				t.Errorf("interrupted() = %v for a line that ended with %v", l.interrupted(), got)
			}
			if l.shellEnded() != errors.Is(tt.want, errShellEnded) {
				t.Errorf("shellEnded() = %v for a line that ended with %v", l.shellEnded(), got)
			}
		})
	}
}

// A line's status is its result's unless the shell's ending cut it off:
// 128 plus the signal the shell's cause names, SIGTERM when it names none.
// Ctrl+C's status is the executor's (final, 130), as is a line's that the
// shell ended only after it was over.
func TestLineContext_Status(t *testing.T) {
	result := &executor.Result{ExitCode: 7}
	tests := []struct {
		name  string
		drive func(l *lineContext, endShell func(error))
		want  int
	}{
		{"running", func(*lineContext, func(error)) {}, 7},
		{"Ctrl+C", func(l *lineContext, _ func(error)) { l.interrupt() }, 7},
		{"the shell's SIGHUP", func(l *lineContext, end func(error)) {
			end(signalCause(syscall.SIGHUP))
			ended(l)
		}, 128 + int(syscall.SIGHUP)},
		{"the shell's end naming no signal", func(l *lineContext, end func(error)) {
			end(errShellEnded)
			ended(l)
		}, 128 + int(syscall.SIGTERM)},
		{"the shell ended after the line", func(l *lineContext, end func(error)) {
			l.returned(nil)
			end(signalCause(syscall.SIGHUP))
		}, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shell, endShell := context.WithCancelCause(context.Background())
			defer endShell(nil)
			l := newLineContext(shell)
			defer l.end(errLineOver)
			tt.drive(l, endShell)
			if got := l.status(result); got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

// heapInUse is the live heap after a full collection.
func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// An ended line keeps nothing: neither the shell's context nor a goroutine
// holds it, however it ended. A kept line that never ended leaked about
// 300 bytes on the shell's context per line.
func TestLineContext_EndedLinesKeepNothing(t *testing.T) {
	shell, endShell := context.WithCancel(context.Background())
	defer endShell()
	run := func(n int) {
		for i := 0; i < n; i++ {
			l := newLineContext(shell)
			switch i % 3 {
			case 0:
				l.returned(nil)
			case 1:
				jobsDone := make(chan struct{})
				l.returned(jobsDone)
				close(jobsDone)
				if ended(l) == nil {
					t.Fatal("a kept line did not end when its last job did")
				}
			case 2:
				l.interrupt()
				l.returned(make(chan struct{}))
			}
		}
	}
	run(300)
	goroutines, before := runtime.NumGoroutine(), heapInUse()
	const n = 30000
	run(n)
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > goroutines && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g := runtime.NumGoroutine(); g > goroutines {
		t.Errorf("%d goroutines left after the lines ended, %d before", g, goroutines)
	}
	if grew := int64(heapInUse()) - int64(before); grew > 20*n {
		t.Errorf("heap grew %d bytes over %d ended lines (%d B/line): the shell keeps them", grew, n, grew/n)
	}
}
