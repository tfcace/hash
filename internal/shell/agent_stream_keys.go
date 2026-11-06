package shell

import (
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// streamKeyWatcher reads the keyboard while a ?? reply streams. The terminal
// stays cooked during a turn so Ctrl+C reaches hash as SIGINT, which also
// left keys echoing into the reply: Esc printed "^[" and did nothing. The
// watcher turns echo and line buffering off (signals stay on), cancels the
// turn on a lone Esc, and swallows other keys so they stay off the screen.
type streamKeyWatcher struct {
	onEsc func()
	poll  func(fd int, timeout time.Duration) (bool, error)
	read  func([]byte) (int, error)

	mu     sync.Mutex // held across a poll and read; Pause takes it
	paused bool

	stop    chan struct{}
	done    chan struct{}
	restore func()
}

func newStreamKeyWatcher(onEsc func()) *streamKeyWatcher {
	return &streamKeyWatcher{
		onEsc: onEsc,
		poll:  pollStdin,
		read:  os.Stdin.Read,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// watchStreamKeys starts a watcher on stdin, or returns nil when stdin is
// not a terminal. All methods are safe on nil.
func watchStreamKeys(onEsc func()) *streamKeyWatcher {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO | unix.ICANON
	quiet.Cc[unix.VMIN] = 1
	quiet.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet); err != nil {
		return nil
	}

	w := newStreamKeyWatcher(onEsc)
	w.restore = func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, old) }
	w.start(fd)
	return w
}

func (w *streamKeyWatcher) start(fd int) {
	go func() {
		defer close(w.done)
		buf := make([]byte, 16)
		for {
			select {
			case <-w.stop:
				return
			default:
			}

			w.mu.Lock()
			if w.paused {
				w.mu.Unlock()
				time.Sleep(10 * time.Millisecond)
				continue
			}
			ready, err := w.poll(fd, 50*time.Millisecond)
			n := 0
			if err == nil && ready {
				n, _ = w.read(buf)
			}
			w.mu.Unlock()

			// A lone ESC byte is the Esc key; arrows and Alt+key arrive as
			// longer sequences in one read.
			if n == 1 && buf[0] == 0x1b && w.onEsc != nil {
				w.onEsc()
			}
		}
	}()
}

// Pause stops reading until Resume, returning once no read is in flight, so
// a prompt that reads its own key gets it.
func (w *streamKeyWatcher) Pause() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.paused = true
	w.mu.Unlock()
}

// Resume restarts reading after Pause.
func (w *streamKeyWatcher) Resume() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.paused = false
	w.mu.Unlock()
}

// Stop ends the watcher and restores the terminal mode it changed.
func (w *streamKeyWatcher) Stop() {
	if w == nil {
		return
	}
	close(w.stop)
	<-w.done
	if w.restore != nil {
		w.restore()
	}
}

// watchTurnKeys starts the Esc watcher for the reply about to stream and
// returns the function that stops it. Esc cancels the turn exactly as
// Ctrl+C does.
func (s *Shell) watchTurnKeys() (stop func()) {
	cancel := s.turnCancel
	if cancel == nil {
		return func() {}
	}
	w := watchStreamKeys(func() { s.handleAgentInterrupt(cancel) })
	s.streamKeys.Store(w)
	return func() {
		s.streamKeys.Store(nil)
		w.Stop()
	}
}
