package executor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// pipeEnds is a pipe whose write end is a non-blocking descriptor, as a
// PTY master is to the key copy.
func pipeEnds(t *testing.T) (r *os.File, w int) {
	t.Helper()
	r, wf, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); wf.Close() })
	return r, nonblockingFD(wf)
}

// The copy of the terminal's keys to a PTY command stops as soon as it is
// told to: the command's end waits for it (the leader reads the keys left
// on the PTY only after it), so every PTY command paid the copy's poll
// interval, up to 50 ms, before the next prompt.
func TestStdinCopy_StopsAtOnce(t *testing.T) {
	keysR, keysW, err := os.Pipe() // a terminal no one types at
	if err != nil {
		t.Fatal(err)
	}
	defer keysR.Close()
	defer keysW.Close()
	_, pty := pipeEnds(t)
	stopped := make(chan time.Time, 1)
	keys := startKeyCopy(pty, keysR, nil, nil, keyHooks{ended: func(error) { stopped <- time.Now() }})
	time.Sleep(10 * time.Millisecond) // the copy waits for a key
	stop := time.Now()
	go keys.stop()
	select {
	case at := <-stopped:
		if took := at.Sub(stop); took > 10*time.Millisecond {
			t.Errorf("the copy stopped %v after it was told to, want at once", took.Round(time.Millisecond))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the copy never stopped")
	}
}

// The copy copies the keys it had and the keys typed until its stop, and
// leaves the ones typed after on the terminal.
func TestStdinCopy_CopiesUntilStopped(t *testing.T) {
	keysR, keysW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer keysR.Close()
	defer keysW.Close()
	ptyR, pty := pipeEnds(t)
	ended := make(chan error, 1)
	released := &closeCount{}
	keys := startKeyCopy(pty, keysR, []byte("ahead "), released, keyHooks{ended: func(err error) { ended <- err }})
	_, _ = keysW.WriteString("abc")
	got := make([]byte, len("ahead abc"))
	if _, err := io.ReadFull(ptyR, got); err != nil {
		t.Fatal(err)
	}
	held := keys.stop()
	held = append(held, keys.stop()...) // a second stop is no panic, and the same
	_, _ = keysW.WriteString("def")
	_ = keysW.Close()
	rest, _ := io.ReadAll(keysR)
	if string(got) != "ahead abc" || len(held) != 0 || string(rest) != "def" || !errors.Is(<-ended, errCopyDone) || released.n != 1 {
		t.Errorf("copied %q, held %q, left %q, released %d times; want ahead abc copied, def left, released once",
			got, held, rest, released.n)
	}
}

// Keys a full PTY has no room for are held, and the stop returns them, in
// order: the copy never blocks in a write, and reads the terminal all the
// while (a double Ctrl+C still reaches it).
func TestStdinCopy_HoldsTheKeysAFullPTYCannotTake(t *testing.T) {
	keysR, keysW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer keysR.Close()
	defer keysW.Close()
	_, pty := pipeEnds(t)
	for { // a PTY whose input queue is full
		if n, err := unix.Write(pty, bytes.Repeat([]byte{'.'}, 4096)); n <= 0 || err != nil {
			break
		}
	}
	var read syncBuffer
	keys := startKeyCopy(pty, keysR, []byte("xyz."), nil, keyHooks{onInput: func(b []byte) { _, _ = read.Write(b) }})
	_, _ = keysW.WriteString("abc")
	for deadline := time.Now().Add(5 * time.Second); read.String() != "abc" && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	held := keys.stop()
	if read.String() != "abc" || string(held) != "xyz.abc" {
		t.Errorf("read %q, held %q; want abc read, and held after the keys the copy had", read.String(), held)
	}
}

// closeCount counts its closes.
type closeCount struct{ n int }

func (c *closeCount) Close() error {
	c.n++
	return nil
}

// Output a PTY command's PTY still has once the command ended (a child it
// left holds the PTY's terminal, which on Linux does not hang up with the
// leader) is cut off after ptyStopGrace: no failure of the command. The
// cut-off copy's end was taken for the command's error, status 1, and the
// line ended there.
func TestPTYRun_OutputCutOffAfterTheEndIsNoFailure(t *testing.T) {
	t.Parallel()
	outR, outW, err := os.Pipe() // a PTY whose terminal someone still holds
	if err != nil {
		t.Fatal(err)
	}
	defer outW.Close()
	r := &ptyRun{ptmx: outR, phase: ptyExited}
	r.output = startOutputCopy(io.Discard, nonblockingFD(outR), nil, nil)
	if err := r.finish(); r.phase != ptyDrained || err != nil {
		t.Errorf("phase %d, status %v; want drained, no error", r.phase, err)
	}
}

// slowTerminal is a terminal that takes what it is shown slowly: a
// millisecond for each 256 bytes.
type slowTerminal struct{ syncBuffer }

func (s *slowTerminal) Write(p []byte) (int, error) {
	time.Sleep(time.Duration(len(p)/256) * time.Millisecond)
	return s.syncBuffer.Write(p)
}

// drainWithin drains r (ptyExited) and fails the test unless it ends
// within limit.
func drainWithin(t *testing.T, r *ptyRun, limit time.Duration) {
	t.Helper()
	drained := make(chan struct{})
	go func() { r.drain(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(limit):
		t.Fatalf("the drain did not end within %v", limit)
	}
}

// Output the PTY still has as the command ended reaches a terminal that
// takes it slowly, all of it: the copy stops once the PTY has shown
// nothing for ptyStopGrace, not ptyStopGrace after the end, which cut off
// the tail of a long output (Linux, where the PTY's terminal outlives its
// leader and keeps what it was shown).
func TestPTYRun_OutputTheCommandLeftReachesASlowTerminal(t *testing.T) {
	t.Parallel()
	outR, outW, err := os.Pipe() // a PTY whose terminal a child still holds
	if err != nil {
		t.Fatal(err)
	}
	defer outW.Close()
	want := bytes.Repeat([]byte("0123456789abcde\n"), 6<<10) // 96 KB, ~400 ms for the terminal
	go func() { _, _ = outW.Write(want) }()
	term := &slowTerminal{}
	r := &ptyRun{ptmx: outR, phase: ptyExited}
	r.output = startOutputCopy(term, nonblockingFD(outR), nil, nil)
	drainWithin(t, r, ptyDrainCap+time.Second)
	if got := term.String(); got != string(want) || r.finish() != nil {
		t.Errorf("the terminal got %d of %d bytes, status %v; want all of them, no error", len(got), len(want), r.finish())
	}
}

// A child the command left that never stops writing to the PTY holds up
// the command's end no longer than ptyDrainCap.
func TestPTYRun_OutputOfAChildThatWritesForeverIsCutOff(t *testing.T) {
	t.Parallel()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outW.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = outW.Write([]byte("y\n"))
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	r := &ptyRun{ptmx: outR, phase: ptyExited}
	r.output = startOutputCopy(io.Discard, nonblockingFD(outR), nil, nil)
	start := time.Now()
	drainWithin(t, r, ptyDrainCap+time.Second)
	if took := time.Since(start); took < ptyDrainCap-50*time.Millisecond {
		t.Errorf("the copy of a child's endless output stopped after %v, want ptyDrainCap (%v)", took, ptyDrainCap)
	}
}

// outOfDescriptors fills the process's descriptor table, under a lower
// limit for the while, and returns the function that frees it again.
func outOfDescriptors(t *testing.T) (free func()) {
	t.Helper()
	var saved unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &saved); err != nil {
		t.Skip(err)
	}
	lowered := saved
	lowered.Cur = 1024
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &lowered); err != nil {
		t.Skip(err)
	}
	var fds []int
	for {
		fd, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			break
		}
		fds = append(fds, fd)
	}
	return func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
		_ = unix.Setrlimit(unix.RLIMIT_NOFILE, &saved)
	}
}

// A PTY command's output copy that started with no pipe to be had (out of
// descriptors) ends all the same once the command ended and a child it
// left holds the PTY: nothing could stop it, and the drain waited for it
// forever.
func TestPTYRun_OutputCopyWithNoPipeEnds(t *testing.T) {
	outR, outW, err := os.Pipe() // a PTY whose terminal a child still holds
	if err != nil {
		t.Fatal(err)
	}
	defer outW.Close()
	r := &ptyRun{ptmx: outR, phase: ptyExited}
	free := outOfDescriptors(t)
	r.output = startOutputCopy(io.Discard, nonblockingFD(outR), nil, nil)
	free()
	drainWithin(t, r, ptyDrainCap+time.Second)
}

// A key copy that starts with no pipe to be had (out of descriptors)
// copies nothing and has stopped already: it released the leader, and
// its stop returns the keys it had.
func TestStdinCopy_WithNoPipeHasStopped(t *testing.T) {
	keysR, keysW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer keysR.Close()
	defer keysW.Close()
	_, pty := pipeEnds(t)
	released := &closeCount{}
	free := outOfDescriptors(t)
	keys := startKeyCopy(pty, keysR, []byte("ahead"), released, keyHooks{})
	free()
	if held := keys.stop(); string(held) != "ahead" || released.n != 1 {
		t.Errorf("held %q, released %d times; want ahead held, released once", held, released.n)
	}
}
