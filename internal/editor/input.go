// internal/editor/input.go
package editor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/trace"
	"golang.org/x/sys/unix"
)

// safeUintToInt converts uint to int, capping at math.MaxInt to prevent overflow.
func safeUintToInt(u uint) int {
	if u > math.MaxInt {
		return math.MaxInt
	}
	return int(u)
}

// DefaultMaxPasteSize is the default maximum size of pasted content (10MB).
// Prevents memory exhaustion from extremely large pastes.
const DefaultMaxPasteSize uint = 10 * 1024 * 1024

// InputReader reads keys from a terminal. The shell keeps one for its whole
// life and every reader of the terminal goes through it: keys one reader
// read but did not take (UnreadKey) come first for the next one.
//
// Bytes come from one queue: bytes a key read past and gave back, then the
// input itself. One tokenizer (readKey) turns them into keys, so every
// reader of the terminal follows the same grammar. Nothing is read before
// a reader asks for a key: typeahead stays on the terminal, where a
// program the shell runs next reads it as its own input. Inside a sequence each byte is waited for at most
// escTimeout: a terminal sends a sequence in one write, so a pause means the
// key is complete (a bare ESC is Escape, ESC [ is Alt+[) and the next key is
// not swallowed into it. For the same reason an ESC that came alone, then
// a key or bytes no known sequence makes (Esc, then O and text in vi), is
// Esc and those keys (typedAfterEsc): only ESC and a key in one write is
// an Alt key.
type InputReader struct {
	in           io.Reader
	file         *os.File // in, when it is a file whose readiness can be polled (a terminal or pipe); nil otherwise
	fd           int      // file's descriptor
	escTimeout   time.Duration
	maxPasteSize uint // Maximum paste size in bytes

	unreadKeys []Key  // keys read but not delivered, returned before any byte
	queue      []byte // bytes read but not yet tokenized
	err        error  // a read error met inside a key, returned in place of the next one
	paste      pasteState
	seq        []byte // scratch: the key being tokenized
	one        [1]byte

	now      func() time.Time // the clock LastRead and Now read
	lastRead time.Time        // when a key's first byte last came from in; zero before the first

	ownerMu sync.Mutex
	owner   *claim // who reads the keys now (Claim); nil when no one does
}

// claim is one reader's hold on the keys. Its release frees the keys only
// while the claim is still theirs: a claim Reset took back stays released
// whatever its holder does later.
type claim struct {
	who string
}

// name is who holds c, or "" for no claim.
func (c *claim) name() string {
	if c == nil {
		return ""
	}
	return c.who
}

// ErrInputBusy is a claim on a reader another reader holds.
var ErrInputBusy = errors.New("key reader busy")

// Claim makes who the reader's one reader until release is called. It
// fails with ErrInputBusy while another holds it: two readers taking keys
// from one terminal would split them between them. release frees this
// claim only: repeating it, or calling it after Reset took the claim back,
// leaves whoever holds the keys now alone.
func (r *InputReader) Claim(who string) (release func(), err error) {
	r.ownerMu.Lock()
	defer r.ownerMu.Unlock()
	if r.owner != nil {
		return nil, fmt.Errorf("%w: %s asked while %s reads", ErrInputBusy, who, r.owner.name())
	}
	c := &claim{who: who}
	r.owner = c
	return func() {
		r.ownerMu.Lock()
		defer r.ownerMu.Unlock()
		if r.owner == c {
			r.owner = nil
		}
	}, nil
}

// Reset drops whatever claim the reader holds and reports whose it was
// ("" when none); that claim's release does nothing from then on. It is
// for the shell's top reader, the prompt: a claim still held when the
// prompt starts can only be one that leaked. It is a safety net, not a
// stop: a goroutine the leaked holder left reading keys is not stopped,
// and may still take a key from the prompt.
func (r *InputReader) Reset() (was string) {
	r.ownerMu.Lock()
	defer r.ownerMu.Unlock()
	was, r.owner = r.owner.name(), nil
	return was
}

// Owner is who reads the keys now, or "" when no one does.
func (r *InputReader) Owner() string {
	r.ownerMu.Lock()
	defer r.ownerMu.Unlock()
	return r.owner.name()
}

// NewInputReader creates a new input reader.
func NewInputReader(in io.Reader) *InputReader {
	r := &InputReader{
		in:           in,
		escTimeout:   50 * time.Millisecond,
		maxPasteSize: DefaultMaxPasteSize,
		now:          time.Now,
	}
	if f, ok := in.(*os.File); ok {
		r.file, r.fd = f, int(f.Fd())
	}
	return r
}

// File is the file the reader reads (the terminal, for the shell's), or
// nil when it reads something else.
func (r *InputReader) File() *os.File {
	return r.file
}

// LastRead is when the reader last read the first byte of a key from its
// input: fresh bytes, typed then. Keys given back (UnreadKeys) and bytes a
// key read past do not count: they were read with the key before them. It
// is zero before the first key.
func (r *InputReader) LastRead() time.Time {
	return r.lastRead
}

// Now is the time on the clock LastRead reads.
func (r *InputReader) Now() time.Time {
	return r.now()
}

// SetClock makes now the clock LastRead and Now read, for tests that type
// at a pace of their own. Set it before the first read.
func (r *InputReader) SetClock(now func() time.Time) {
	r.now = now
}

// timed reports whether the reader can wait for input with a timeout.
func (r *InputReader) timed() bool {
	return r.file != nil
}

// SetMaxPasteSize sets the maximum paste size in bytes.
func (r *InputReader) SetMaxPasteSize(size uint) {
	if size > 0 {
		r.maxPasteSize = size
	}
}

// hasDataAvailableWithTimeout checks if data is ready within the timeout duration.
// A timeout of 0 means poll immediately without waiting. The end of the
// input counts as ready: the read returns it. A signal (SIGWINCH) does not
// cut the wait short.
func hasDataAvailableWithTimeout(fd int, timeout time.Duration) bool {
	if fd < 0 || fd > math.MaxInt32 {
		return false
	}
	deadline := time.Now().Add(timeout)
	for {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		ms := int(time.Until(deadline).Milliseconds())
		if ms < 0 || timeout == 0 {
			ms = 0
		}
		n, err := unix.Poll(fds, ms)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			return false
		}
		return fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0
	}
}

// ReadKey reads and parses the next key.
func (r *InputReader) ReadKey() (Key, error) {
	return r.ReadKeyInterruptible(nil)
}

// ReadKeyInterruptible reads and parses the next key, checking done channel periodically.
// If done is closed, returns context.Canceled error.
// This prevents goroutines from blocking indefinitely on stdin when the editor exits.
// Sequences that parse to no key (terminal reports, unbound keys) are skipped.
func (r *InputReader) ReadKeyInterruptible(done <-chan struct{}) (Key, error) {
	if len(r.unreadKeys) > 0 {
		key := r.unreadKeys[0]
		r.unreadKeys = r.unreadKeys[1:]
		return key, nil
	}
	for {
		key, err := r.readKey(done)
		if err != nil || key != (Key{}) {
			return key, err
		}
	}
}

// UnreadKeys gives back keys that were read but not used: the next reads
// return them first, in order. The keys themselves go back, not their
// bytes, so they are not tokenized (or a paste cut to size) a second time.
func (r *InputReader) UnreadKeys(keys ...Key) {
	if len(keys) > 0 {
		r.unreadKeys = append(append([]Key(nil), keys...), r.unreadKeys...)
	}
}

// DropHeld drops every key held for the next reader: keys given back, and
// bytes read past a key. Input still waiting on the terminal stays there.
func (r *InputReader) DropHeld() {
	r.unreadKeys, r.queue = nil, nil
}

// Typeahead takes every key typed so far: keys given back, then those
// whose bytes are already waiting. A reader that asks the user a question
// sets them aside (and gives them back after), so no key typed before the
// question was on screen answers it.
func (r *InputReader) Typeahead() []Key {
	keys := r.unreadKeys
	r.unreadKeys = nil
	for len(r.queue) > 0 || r.err == nil && r.timed() && hasDataAvailableWithTimeout(r.fd, 0) {
		key, err := r.readKey(nil)
		if err != nil {
			break
		}
		if key != (Key{}) {
			keys = append(keys, key)
		}
	}
	return keys
}

// readKey tokenizes the next key: its first byte waits as long as it takes,
// each later one at most escTimeout. A byte that cannot belong to the key
// goes back to the queue and starts the next one.
func (r *InputReader) readKey(done <-chan struct{}) (Key, error) {
	if r.paste == pasteCut {
		r.dropCutPaste()
	}
	b, err := r.first(done)
	if err != nil {
		trace.Editor("key_read", map[string]any{"error": err.Error()})
		return Key{}, err
	}
	r.seq = append(r.seq[:0], b)
	escAlone := b == 0x1b && !r.ready()
	overlong := false
	for state := start(b); state != tokDone; {
		b, ok := r.more()
		if !ok {
			break // a pause, or the end of the input: the key is what came
		}
		var keep bool
		if state, keep = state.accept(r.seq, b, r.timed()); !keep {
			r.unread([]byte{b})
			break
		}
		// Past the cap the sequence is still consumed to its end, in the
		// same state, so its tail is not typed as keys; it is discarded.
		if len(r.seq) < maxSequence {
			r.seq = append(r.seq, b)
		} else {
			overlong = true
		}
	}
	seq := r.seq

	if overlong {
		trace.Editor("key_read", map[string]any{"discarded_overlong": len(seq)})
		return Key{}, nil
	}
	if isPasteStart(seq) {
		return r.readPaste(), nil
	}
	key := ParseKey(seq)
	if escAlone && typedAfterEsc(seq, key) {
		r.unread(seq[1:])
		key = Key{Special: KeyEscape}
	}
	trace.Editor("key_read", map[string]any{
		"raw":    seq,
		"parsed": keyString(key),
	})
	return key, nil
}

// typedAfterEsc reports whether seq, read as key, is Esc and keys typed
// after it rather than one key: its ESC came alone, the rest after it, not
// with it as a terminal sends a sequence. One key after the ESC is that
// (a character, Enter, Backspace: what one write makes an Alt chord), and
// so is a sequence nothing knows. A key the terminal sends as a sequence
// (Up) stays that key, and a terminal's report stays a report, whatever
// pause split its bytes.
func typedAfterEsc(seq []byte, key Key) bool {
	switch {
	case len(seq) < 2:
		return false // the ESC alone: Escape
	case len(seq) == 2 || seq[1] >= utf8.RuneSelf: // ESC and one character (a multibyte one too)
		return true
	case key == (Key{}) && len(seq) > 2:
		return seq[1] == '[' && !isReport(seq[2:]) || seq[1] == 'O'
	}
	return false
}

// tokenState is where the tokenizer is inside a key's bytes.
type tokenState int

const (
	tokDone      tokenState = iota // the key is complete
	tokEsc                         // after ESC: a sequence or an Alt key may follow
	tokCSI                         // after ESC [: parameters up to a final byte
	tokSS3                         // after ESC O: one code byte
	tokUTF8                        // inside a multibyte character, alone or after ESC
	tokOSC                         // after ESC ]: a digit makes it a reply, else it was Alt+]
	tokDCS                         // after ESC P: a reply's first byte makes it one, else it was Alt+P
	tokAPC                         // after ESC _: a kitty graphics reply (G), else it was Alt+_
	tokString                      // inside a terminal's reply string, up to BEL or ESC \
	tokStringEsc                   // an ESC inside a string: \ ends it
)

// maxSequence bounds the bytes a key keeps. A longer sequence (an OSC 52
// clipboard reply) is read to its end and discarded.
const maxSequence = 256

// start is the state after a key's first byte.
func start(b byte) tokenState {
	switch {
	case b == 0x1b:
		return tokEsc
	case utf8Len(b) > 1:
		return tokUTF8
	}
	return tokDone // ASCII, or a byte no character starts with (discarded)
}

// accept feeds b to a key whose bytes so far are seq. It returns the state
// after b, and whether b belongs to this key at all; one that does not is
// given back to start the next key.
func (s tokenState) accept(seq []byte, b byte, timed bool) (tokenState, bool) {
	switch s {
	case tokEsc:
		return acceptAfterEsc(b, timed)
	case tokCSI:
		return acceptCSI(seq, b)
	case tokSS3:
		return tokDone, b >= 0x20
	case tokUTF8:
		return acceptUTF8(seq, b)
	case tokOSC:
		// OSC replies start with their number: 11;rgb:..., 52;c;...
		return tokString, b >= '0' && b <= '9'
	case tokDCS:
		// DCS replies: XTVERSION (>|), DECRPTUI (!|), DECRQSS (1$r, 0$r),
		// XTGETTCAP (1+r).
		return tokString, strings.IndexByte(">!$01", b) >= 0
	case tokAPC:
		return tokString, b == 'G'
	case tokString:
		switch b {
		case 0x07:
			return tokDone, true
		case 0x1b:
			return tokStringEsc, true
		}
		return tokString, true
	case tokStringEsc:
		// ESC \ ends the string; anything else after ESC cuts it short.
		return tokDone, b == '\\'
	}
	return tokDone, false
}

func acceptAfterEsc(b byte, timed bool) (tokenState, bool) {
	switch {
	case b == '[':
		return tokCSI, true
	case b == 'O':
		return tokSS3, true
	case b == ']':
		// OSC, DCS or APC may start a reply to a terminal query (background
		// color, version) that arrived late, which is discarded; the next
		// byte tells it from Alt+], Alt+P or Alt+_ typed before more text.
		return tokOSC, true
	case b == 'P':
		return tokDCS, true
	case b == '_':
		return tokAPC, true
	case b == '\r' || b == '\n' || b == 0x08:
		// Alt+Enter (some terminals send it for Shift+Enter) or
		// Alt+Backspace where Backspace sends ^H, when the reader can tell
		// the two arrived together; else Escape, then the key.
		return tokDone, timed
	case b < 0x20:
		return tokDone, false // Escape, then that control key (another ESC included)
	case b >= utf8.RuneSelf && utf8Len(b) == 0:
		return tokDone, false // Escape, then a stray byte (discarded)
	case utf8Len(b) > 1:
		return tokUTF8, true // Alt + a multibyte character
	}
	return tokDone, true // Alt + a character
}

func acceptCSI(seq []byte, b byte) (tokenState, bool) {
	switch {
	case b < 0x20:
		return tokDone, false // a control (a new ESC) cuts the sequence short
	case len(seq) == 2 && b == '[':
		return tokCSI, true // ESC [ [: the Linux console's F-key prefix
	case b >= 0x40 && b <= 0x7e:
		return tokDone, true // the final byte
	}
	return tokCSI, true
}

func acceptUTF8(seq []byte, b byte) (tokenState, bool) {
	if b&0xc0 != 0x80 {
		return tokDone, false // not a continuation: the character was cut
	}
	lead := 0
	if seq[0] == 0x1b {
		lead = 1
	}
	if len(seq)+1-lead == utf8Len(seq[lead]) {
		return tokDone, true
	}
	return tokUTF8, true
}

// utf8Len is how many bytes a UTF-8 character starting with lead has: 1 for
// ASCII, 2-4 for a lead byte, 0 for a byte no character starts with.
func utf8Len(lead byte) int {
	switch {
	case lead < 0x80:
		return 1
	case lead >= 0xc2 && lead <= 0xdf:
		return 2
	case lead >= 0xe0 && lead <= 0xef:
		return 3
	case lead >= 0xf0 && lead <= 0xf4:
		return 4
	}
	return 0
}

// first returns the first byte of the next key, waiting as long as it takes.
func (r *InputReader) first(done <-chan struct{}) (byte, error) {
	if b, ok := r.dequeue(); ok {
		return b, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	if err := r.waitReadable(done); err != nil {
		return 0, err
	}
	n, err := r.in.Read(r.one[:])
	if n == 1 {
		r.err = err
		r.lastRead = r.now()
		return r.one[0], nil
	}
	if err == nil {
		err = io.EOF
	}
	return 0, err
}

// ready reports whether the next byte is already here: queued, or waiting
// on the input. A reader that cannot be polled counts as ready, so its
// bytes keep forming sequences as they always did.
func (r *InputReader) ready() bool {
	return len(r.queue) > 0 || r.err != nil || !r.timed() || hasDataAvailableWithTimeout(r.fd, 0)
}

// more returns the next byte inside a key, or false when none came within
// escTimeout (or the input ended; the error is kept for the next key).
// Readers that cannot be polled are read directly.
func (r *InputReader) more() (byte, bool) {
	if b, ok := r.dequeue(); ok {
		return b, true
	}
	if r.err != nil {
		return 0, false
	}
	if r.timed() && !hasDataAvailableWithTimeout(r.fd, r.escTimeout) {
		return 0, false
	}
	n, err := r.in.Read(r.one[:])
	if err == nil && n == 0 {
		err = io.EOF
	}
	r.err = err
	return r.one[0], n == 1
}

func (r *InputReader) dequeue() (byte, bool) {
	if len(r.queue) == 0 {
		return 0, false
	}
	b := r.queue[0]
	r.queue = r.queue[1:]
	return b, true
}

// unread puts bytes back at the front of the queue.
func (r *InputReader) unread(b []byte) {
	if len(b) > 0 {
		r.queue = append(append([]byte(nil), b...), r.queue...)
	}
}

// waitReadable blocks until a TTY has input, polling so a closed done
// returns context.Canceled instead of leaving the read stuck on stdin. Other
// readers are read directly.
func (r *InputReader) waitReadable(done <-chan struct{}) error {
	if !r.timed() || done == nil {
		return nil
	}
	const pollInterval = 50 * time.Millisecond
	for {
		select {
		case <-done:
			return context.Canceled
		default:
		}
		if hasDataAvailableWithTimeout(r.fd, pollInterval) {
			break
		}
	}
	// Re-check done after poll to avoid reading stdin when the editor is
	// shutting down (e.g., for Ctrl+R -> bubbletea handoff).
	select {
	case <-done:
		return context.Canceled
	default:
		return nil
	}
}

// specialKeyNames maps special key codes to their display names.
var specialKeyNames = map[KeyCode]string{
	KeyEnter:     "Enter",
	KeyTab:       "Tab",
	KeyBackspace: "Backspace",
	KeyDelete:    "Delete",
	KeyEscape:    "Escape",
	KeyUp:        "Up",
	KeyDown:      "Down",
	KeyLeft:      "Left",
	KeyRight:     "Right",
	KeyHome:      "Home",
	KeyEnd:       "End",
	KeyPageUp:    "PageUp",
	KeyPageDown:  "PageDown",
	KeyPaste:     "Paste",
}

// keyString returns a human-readable string for a Key.
func keyString(k Key) string {
	var parts []string
	if k.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if k.Alt {
		parts = append(parts, "Alt")
	}
	if k.Shift {
		parts = append(parts, "Shift")
	}

	keyName := specialKeyNames[k.Special]
	if keyName == "" && k.Special == KeyNone && k.Rune != 0 {
		keyName = string(k.Rune)
	} else if keyName == "" && k.Special != KeyNone {
		keyName = "Unknown"
	}

	if keyName != "" {
		parts = append(parts, keyName)
	}
	if len(parts) == 0 {
		return "None"
	}
	return strings.Join(parts, "+")
}

// pasteEnd is the bracketed paste end marker.
var pasteEnd = []byte("\x1b[201~")

// pasteState is where the reader is with bracketed pastes. A paste in
// progress is read to its end in one call (readPaste) whatever done says: a
// prompt torn down mid-paste must not leave the tail to be read as typed
// keys, newlines and all. The paste goes to the next prompt instead.
type pasteState int

const (
	pasteNone pasteState = iota
	// pasteCut: the last paste paused for pasteIdleTimeout before its end
	// marker and was delivered as it stood. Its late rest is dropped up to
	// the marker, or until the input pauses again (the marker was lost).
	pasteCut
)

// pasteIdleTimeout is how long a paste may pause before it is cut. A
// terminal sends a paste in one go; a pause this long means its end marker
// is not coming.
const pasteIdleTimeout = 300 * time.Millisecond

// readPaste reads a bracketed paste's content up to the end marker.
// Content past maxPasteSize is dropped: the paste keeps its head, what the
// user sees first. A paste that pauses before its marker is delivered as it
// stands and its rest dropped (pasteCut). An input that ends mid-paste
// delivers what came; the next read reports the end.
func (r *InputReader) readPaste() Key {
	limit := safeUintToInt(r.maxPasteSize)
	var text []byte
	over := false
	ended, err := r.toPasteEnd(func(b []byte) {
		over = over || len(text)+len(b) > limit
		text = appendCapped(text, b, limit)
	})
	if !ended && err == nil {
		r.paste = pasteCut
	}
	return Key{Special: KeyPaste, PasteText: string(text), PasteCut: over || !ended}
}

// dropCutPaste drops the rest of a cut paste through its end marker.
func (r *InputReader) dropCutPaste() {
	r.paste = pasteNone
	_, _ = r.toPasteEnd(func([]byte) {}) // a pause or the input's end stops it too
}

// toPasteEnd reads in blocks through the paste end marker, passing what
// comes before it to take, and gives the bytes after it back to the queue.
// Only a possible start of the marker is held back between blocks. It
// reports false, with the held bytes back in the queue, when the input
// paused for pasteIdleTimeout first, and with the error when it ended.
func (r *InputReader) toPasteEnd(take func([]byte)) (bool, error) {
	var held, scan []byte
	var chunk [4096]byte
	for {
		data := r.queue
		r.queue = nil
		if len(data) == 0 {
			if r.err != nil {
				return false, r.err
			}
			if r.timed() && !hasDataAvailableWithTimeout(r.fd, pasteIdleTimeout) {
				r.unread(held)
				return false, nil
			}
			n, err := r.in.Read(chunk[:])
			if err == nil && n == 0 {
				err = io.EOF
			}
			r.err, data = err, chunk[:n]
		}

		scan = append(append(scan[:0], held...), data...)
		if i := bytes.Index(scan, pasteEnd); i >= 0 {
			take(scan[:i])
			r.unread(scan[i+len(pasteEnd):])
			return true, nil
		}
		keep := markerStartLen(scan)
		take(scan[:len(scan)-keep])
		held = append(held[:0], scan[len(scan)-keep:]...)
	}
}

// markerStartLen is the length of the longest end of b that could begin the
// paste end marker.
func markerStartLen(b []byte) int {
	for n := min(len(b), len(pasteEnd)-1); n > 0; n-- {
		if bytes.HasPrefix(pasteEnd, b[len(b)-n:]) {
			return n
		}
	}
	return 0
}

// appendCapped appends as much of b to text as fits in limit bytes.
func appendCapped(text, b []byte, limit int) []byte {
	room := max(limit-len(text), 0)
	return append(text, b[:min(len(b), room)]...)
}

// isPasteStart checks if the sequence is the bracketed paste start marker.
func isPasteStart(b []byte) bool {
	return string(b) == "\x1b[200~"
}
