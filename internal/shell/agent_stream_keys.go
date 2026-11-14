package shell

import (
	"context"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/tfcace/hash/internal/editor"
)

// streamKeyWatcher owns the keyboard while a ?? request runs. The terminal
// stays cooked during a turn so Ctrl+C reaches hash as SIGINT, which also
// left keys echoing into the reply: Esc printed "^[" and did nothing. The
// watcher turns echo, line buffering and the extended controls (Ctrl+O,
// Ctrl+V) off (signals stay on), cancels the turn on Esc, and keeps every
// other key off the screen and, in order, for whatever reads next: keys
// typed during a turn are typeahead for the next prompt, through the first
// Enter (holdForNextPrompt). A permission prompt asks it for the next key
// instead of reading the terminal itself, so the terminal mode has one
// owner and is restored once.
type streamKeyWatcher struct {
	keys  *editor.InputReader // the shell's key reader; only the watcher's goroutine reads it until Stop returns
	onEsc func()

	mu     sync.Mutex
	phase  watchPhase
	asking *keyAsk      // the prompt waiting for its key, in phase watcherAsking; nil otherwise
	kept   []editor.Key // keys no one here used, given back to the reader by Stop

	stop    chan struct{} // closed by Stop, under mu, as the phase becomes stopped: the read in progress returns
	done    chan struct{} // closed when the reading goroutine has returned
	restore func()
}

// watchPhase is what the watcher does with the next key.
type watchPhase int

const (
	// watcherWatching: Esc cancels the turn and every other key is kept.
	watcherWatching watchPhase = iota
	// watcherAsking: a permission prompt waits for its key (asking): an answer
	// typed once the keys settled answers it, Esc and Ctrl+C at once;
	// any other key is dropped.
	watcherAsking
	// watcherStopped: Stop began, done for good: a key read now is kept, Esc too.
	watcherStopped
)

func newStreamKeyWatcher(keys *editor.InputReader, onEsc func()) *streamKeyWatcher {
	return &streamKeyWatcher{
		keys:  keys,
		onEsc: onEsc,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// watch starts a watcher on the terminal, or returns nil when the keys do
// not come from one or another reader holds them. Stop is safe on nil.
func (k keyboard) watch(onEsc func()) *streamKeyWatcher {
	fd, release, ok := k.claim("turn watcher")
	if !ok {
		return nil
	}
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		release()
		return nil
	}
	quiet := *old
	// IEXTEN off too: macOS reads it as Ctrl+O discarding output
	// (VDISCARD), the rest of the reply. ISIG stays on for Ctrl+C.
	quiet.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN
	quiet.Cc[unix.VMIN] = 1
	quiet.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet); err != nil {
		release()
		return nil
	}

	w := newStreamKeyWatcher(k.keys, onEsc)
	w.restore = func() {
		_ = unix.IoctlSetTermios(fd, ioctlSetTermios, old)
		release()
	}
	w.start()
	return w
}

func (w *streamKeyWatcher) start() {
	go func() {
		defer close(w.done)
		for {
			prev := w.keys.LastRead()
			key, err := w.keys.ReadKeyInterruptible(w.stop)
			if err != nil {
				return // stopped, or the input is gone
			}
			w.key(key, w.keys.LastRead(), prev)
		}
	}()
}

// key handles one key, read at `at`, the key before it at prev, as the
// phase says: the answer to a waiting prompt, Esc canceling the turn, or a
// key kept for the next prompt. An Alt chord is no Esc (isEsc): it is kept.
func (w *streamKeyWatcher) key(key editor.Key, at, prev time.Time) {
	w.mu.Lock()
	var then func() // run once mu is let go: the cancel, or the prompt's note
	switch w.phase {
	case watcherStopped:
		w.kept = append(w.kept, key)
	case watcherAsking:
		then = w.answerLocked(key, at, prev)
	case watcherWatching:
		if isEsc(key) && w.onEsc != nil {
			then = w.onEsc
		} else {
			w.kept = append(w.kept, key)
		}
	}
	w.mu.Unlock()
	if then != nil {
		then()
	}
}

// answerLocked is key at the waiting prompt: its answer once the keys
// settled (settled), Esc and Ctrl+C at once. A key typed sooner is
// dropped, the first one with the prompt's note, which it returns to draw;
// a key that is no answer (an arrow) is dropped too. The caller holds mu.
func (w *streamKeyWatcher) answerLocked(key editor.Key, at, prev time.Time) (note func()) {
	a := w.asking
	if !actsAtOnce(key) && !settled(at, prev, a.asked) {
		return a.droppedLocked()
	}
	if answer, ok := answerKey(key); ok {
		a.answer <- answer
		w.asking, w.phase = nil, watcherWatching
	}
	return nil
}

// isEsc reports whether key is Esc: an ESC that came alone. ESC and a key
// in one write is an Alt chord (Alt+u from a terminal that does not encode
// Alt), as is a key whose Alt the terminal encoded (Alt+Left, ESC[1;3D;
// kitty's Alt+., ESC[46;3u) and ESC CR (some terminals' Shift+Enter): no
// reader takes one for Esc. A quick Esc then a key is Esc, then the key:
// the reader splits it.
func isEsc(key editor.Key) bool {
	return key == editor.Key{Special: editor.KeyEscape}
}

// answerKey is what a key does at a permission prompt: the byte it answers
// with, as permissionAnswerForKey reads it (an ASCII character as typed,
// Enter as CR, Esc as ESC, Ctrl+C as ETX, 0 for any other character or
// one with Ctrl or Alt held: deny). Motion keys (arrows) are no answer.
func answerKey(key editor.Key) (answer byte, ok bool) {
	switch {
	case isEsc(key):
		return 0x1b, true
	case key.Special == editor.KeyEnter:
		return '\r', true
	case key.Special != editor.KeyNone || key.Rune <= 0:
		return 0, false
	case key.Ctrl:
		if key.Rune == 'c' {
			return 0x03, true
		}
		return 0, true
	case key.Alt:
		return 0, true // Alt+y is not y
	case key.Rune < utf8.RuneSelf:
		return byte(key.Rune), true
	}
	return 0, true
}

// keyAsk is a permission prompt's claim on the next key. It is taken before
// the prompt is drawn, so a key typed the moment the prompt appears is
// dropped as typed too soon, not kept for the next prompt.
type keyAsk struct {
	w      *streamKeyWatcher
	answer chan byte // nil when the watcher had already stopped
	asked  time.Time // when the prompt asked, on the key reader's clock
	// note draws tooQuickNote on the prompt, the first time a key is
	// dropped once the prompt is drawn; nil: nothing is drawn.
	note  func()
	state askState // under the watcher's mu
}

// askState is where a permission prompt is: drawn yet or not, and whether
// a key was dropped (and the note drawn for it).
type askState int

const (
	// askUndrawn: the key is claimed, the prompt not drawn yet.
	askUndrawn askState = iota
	// askUndrawnDropped: a key was dropped before the prompt was drawn:
	// the note is owed, drawn with the prompt (drawn).
	askUndrawnDropped
	// askDrawn: the prompt is on screen, no key dropped since.
	askDrawn
	// askNoted: the prompt is on screen with its note.
	askNoted
)

// droppedLocked moves the prompt on for a key dropped as too quick and
// returns the note to draw, once mu is let go: none before the prompt is
// drawn, where the note has nowhere to go, or once it is drawn. The caller
// holds the watcher's mu.
func (a *keyAsk) droppedLocked() (note func()) {
	switch a.state {
	case askUndrawn:
		a.state = askUndrawnDropped
	case askDrawn:
		a.state = askNoted
		return a.note
	}
	return nil
}

// drawn moves the prompt on once it is on screen, drawing the note a key
// dropped before then owes.
func (a *keyAsk) drawn() {
	a.w.mu.Lock()
	var note func()
	switch a.state {
	case askUndrawn:
		a.state = askDrawn
	case askUndrawnDropped:
		a.state, note = askNoted, a.note
	}
	a.w.mu.Unlock()
	if note != nil {
		note()
	}
}

// Ask claims the next key for a permission prompt. Call it before drawing
// the prompt, then Wait for the answer. note draws tooQuickNote on the
// prompt when a key comes before the keys settled; it may be nil.
func (w *streamKeyWatcher) Ask(note func()) *keyAsk {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.phase == watcherStopped {
		return &keyAsk{w: w}
	}
	w.asking = &keyAsk{w: w, answer: make(chan byte, 1), asked: w.keys.Now(), note: note}
	w.phase = watcherAsking
	return w.asking
}

// Wait returns the key that answered the prompt, or 'n' (deny) when ctx ends
// or the watcher stops first. Either way the claim is released. The prompt
// is drawn by the time Wait is called (drawn).
func (a *keyAsk) Wait(ctx context.Context) byte {
	if a.answer == nil {
		return 'n'
	}
	a.drawn()
	select {
	case k := <-a.answer:
		return k
	case <-ctx.Done():
	case <-a.w.done:
	}
	a.release()
	return 'n'
}

// release gives the next key back to the watcher, for a prompt that will
// not wait (its turn moved on before it was drawn).
func (a *keyAsk) release() {
	if a.answer == nil {
		return
	}
	a.w.mu.Lock()
	if a.w.asking == a {
		a.w.asking, a.w.phase = nil, watcherWatching
	}
	a.w.mu.Unlock()
}

// Stop ends the watcher, gives the keys it kept back to the reader for
// whatever reads next, restores the terminal mode it changed and lets go
// of the keys. It is
// safe to repeat: only the first call does anything. It runs on the
// goroutine that reads the keys next, so the reader is never shared.
func (w *streamKeyWatcher) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.phase == watcherStopped {
		w.mu.Unlock()
		return
	}
	w.asking, w.phase = nil, watcherStopped
	close(w.stop)
	w.mu.Unlock()
	<-w.done
	holdForNextPrompt(w.keys, w.kept)
	w.kept = nil
	if w.restore != nil {
		w.restore()
	}
}
