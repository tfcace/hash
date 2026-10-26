package shell

import (
	"sync/atomic"
	"testing"
	"time"
)

// fakeKeys feeds the watcher one queued read per poll.
type fakeKeys struct {
	reads chan []byte
	polls atomic.Int32
}

func (f *fakeKeys) poll(int, time.Duration) (bool, error) {
	f.polls.Add(1)
	if len(f.reads) == 0 {
		time.Sleep(time.Millisecond)
		return false, nil
	}
	return true, nil
}

func (f *fakeKeys) read(buf []byte) (int, error) {
	return copy(buf, <-f.reads), nil
}

func startFakeWatcher(keys *fakeKeys, onEsc func()) *streamKeyWatcher {
	w := newStreamKeyWatcher(onEsc)
	w.poll = keys.poll
	w.read = keys.read
	w.start(0)
	return w
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStreamKeyWatcher_EscCancels(t *testing.T) {
	keys := &fakeKeys{reads: make(chan []byte, 4)}
	var canceled atomic.Bool
	w := startFakeWatcher(keys, func() { canceled.Store(true) })
	defer w.Stop()

	keys.reads <- []byte{0x1b}
	waitFor(t, canceled.Load)
}

// Other keys and escape sequences (arrows, Alt+key) are swallowed, not
// treated as Esc.
func TestStreamKeyWatcher_IgnoresOtherKeys(t *testing.T) {
	keys := &fakeKeys{reads: make(chan []byte, 4)}
	var canceled atomic.Bool
	w := startFakeWatcher(keys, func() { canceled.Store(true) })

	keys.reads <- []byte("a")
	keys.reads <- []byte("\x1b[A")
	keys.reads <- []byte("\x1bx")
	waitFor(t, func() bool { return len(keys.reads) == 0 })
	w.Stop()

	if canceled.Load() {
		t.Error("a non-Esc key canceled the turn")
	}
}

// A permission prompt reads its own key: a paused watcher must not take it.
func TestStreamKeyWatcher_PauseLeavesKeysAlone(t *testing.T) {
	keys := &fakeKeys{reads: make(chan []byte, 4)}
	var canceled atomic.Bool
	w := startFakeWatcher(keys, func() { canceled.Store(true) })
	defer w.Stop()

	w.Pause()
	keys.reads <- []byte{0x1b}
	time.Sleep(50 * time.Millisecond)
	if len(keys.reads) != 1 || canceled.Load() {
		t.Fatal("paused watcher read a key meant for the prompt")
	}

	w.Resume()
	waitFor(t, canceled.Load)
}

func TestStreamKeyWatcher_NilIsSafe(t *testing.T) {
	var w *streamKeyWatcher
	w.Pause()
	w.Resume()
	w.Stop()
}
