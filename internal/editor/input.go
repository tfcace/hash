// internal/editor/input.go
package editor

import (
	"context"
	"io"
	"math"
	"os"
	"strings"
	"time"

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

// InputReader reads keys from a terminal.
type InputReader struct {
	in           io.Reader
	buf          [64]byte // Buffer for escape sequences
	escTimeout   time.Duration
	pending      byte // Byte to return on next read (0 = none)
	hasPending   bool
	pendingKeys  []Key  // Keys to return before reading new input
	pasteBuffer  []byte // Buffer for paste content
	maxPasteSize uint   // Maximum paste size in bytes
}

// NewInputReader creates a new input reader.
func NewInputReader(in io.Reader) *InputReader {
	return &InputReader{
		in:           in,
		escTimeout:   50 * time.Millisecond,
		maxPasteSize: DefaultMaxPasteSize,
	}
}

// SetMaxPasteSize sets the maximum paste size in bytes.
func (r *InputReader) SetMaxPasteSize(size uint) {
	if size > 0 {
		r.maxPasteSize = size
	}
}

// deadlineReader is an interface for readers that support deadlines.
type deadlineReader interface {
	SetReadDeadline(t time.Time) error
}

// supportsDeadlines returns true if the underlying reader supports deadlines.
func (r *InputReader) supportsDeadlines() bool {
	_, ok := r.in.(deadlineReader)
	return ok
}

// readWithTimeout reads with a timeout using select().
// Returns (n, err, timedOut).
func (r *InputReader) readWithTimeout(buf []byte, timeout time.Duration) (int, error, bool) {
	// For *os.File (TTYs), use select() with timeout since SetReadDeadline
	// doesn't work on TTYs ("file type does not support deadline").
	if f, ok := r.in.(*os.File); ok {
		fd := int(f.Fd())
		if hasDataAvailableWithTimeout(fd, timeout) {
			n, err := r.in.Read(buf)
			return n, err, false
		}
		// Timeout - no data available
		return 0, nil, true
	}

	// For non-file readers (e.g., bytes.Reader in tests), just read immediately.
	// These don't support timeouts, so we read whatever is available.
	n, err := r.in.Read(buf)
	return n, err, false
}

// DrainPending reads any immediately available input without blocking.
// This captures characters that may have been typed during terminal mode transitions.
// Call this right after enabling raw mode to recover any "lost" keystrokes.
func (r *InputReader) DrainPending() {
	// Get file descriptor - only works for *os.File
	f, ok := r.in.(*os.File)
	if !ok {
		return
	}
	fd := int(f.Fd())

	// Read any pending input using select() to check availability
	for hasDataAvailable(fd) {
		n, err := r.in.Read(r.buf[:1])
		if err != nil || n == 0 {
			break
		}

		// Parse and queue the key
		var key Key
		if r.buf[0] == 0x1b {
			// Might be start of escape sequence - try to read more
			key = r.drainEscapeSequence(fd)
		} else {
			key = ParseKey(r.buf[:1])
		}

		// Skip no-op keys (terminal responses like DECRPM that were parsed and discarded)
		if key.Special == KeyNone && key.Rune == 0 {
			continue
		}
		r.pendingKeys = append(r.pendingKeys, key)
	}
}

// hasDataAvailable checks if there's data ready to read on fd without blocking.
func hasDataAvailable(fd int) bool {
	return hasDataAvailableWithTimeout(fd, 0)
}

// hasDataAvailableWithTimeout checks if data is ready within the timeout duration.
// A timeout of 0 means poll immediately without waiting.
func hasDataAvailableWithTimeout(fd int, timeout time.Duration) bool {
	var readSet unix.FdSet
	readSet.Set(fd)

	tv := unix.NsecToTimeval(timeout.Nanoseconds())

	_, err := unix.Select(fd+1, &readSet, nil, nil, &tv)
	if err != nil {
		return false
	}

	return readSet.IsSet(fd)
}

// drainEscapeSequence reads an escape sequence during drain.
// Uses a small timeout (2ms) between bytes to avoid splitting multi-byte
// terminal responses (e.g., DECRPM \x1b[?2027;1$y) into partial reads.
func (r *InputReader) drainEscapeSequence(fd int) Key {
	total := 1 // Already have ESC in buf[0]

	for total < len(r.buf) {
		if !hasDataAvailableWithTimeout(fd, 2*time.Millisecond) {
			break
		}
		n, err := r.in.Read(r.buf[total : total+1])
		if err != nil || n == 0 {
			break
		}
		total++
		if isCompleteSequence(r.buf[:total]) {
			break
		}
	}

	return ParseKey(r.buf[:total])
}

// ReadKey reads and parses the next key.
func (r *InputReader) ReadKey() (Key, error) {
	return r.ReadKeyInterruptible(nil)
}

// ReadKeyInterruptible reads and parses the next key, checking done channel periodically.
// If done is closed, returns context.Canceled error.
// This prevents goroutines from blocking indefinitely on stdin when the editor exits.
func (r *InputReader) ReadKeyInterruptible(done <-chan struct{}) (Key, error) {
	// Return any keys that were drained during mode transition
	if len(r.pendingKeys) > 0 {
		key := r.pendingKeys[0]
		r.pendingKeys = r.pendingKeys[1:]
		trace.Editor("key_read", map[string]any{
			"source":  "pending",
			"parsed":  keyString(key),
			"pending": len(r.pendingKeys),
		})
		return key, nil
	}

	// Check for pending byte from previous escape sequence handling
	if r.hasPending {
		r.buf[0] = r.pending
		r.hasPending = false
		key := ParseKey(r.buf[:1])
		trace.Editor("key_read", map[string]any{
			"source": "pending_byte",
			"raw":    []byte{r.pending},
			"parsed": keyString(key),
		})
		return key, nil
	}

	// For TTY file descriptors, use polling to allow checking done channel
	if f, ok := r.in.(*os.File); ok && done != nil {
		fd := int(f.Fd())
		pollInterval := 50 * time.Millisecond
		for {
			// Check done channel first
			select {
			case <-done:
				return Key{}, context.Canceled
			default:
			}

			// Poll for available data with timeout
			if hasDataAvailableWithTimeout(fd, pollInterval) {
				break // Data available, proceed to read
			}
			// No data, loop back to check done channel
		}
	}

	// Read first byte
	n, err := r.in.Read(r.buf[:1])
	if err != nil {
		trace.Editor("key_read", map[string]any{
			"error": err.Error(),
		})
		return Key{}, err
	}
	if n == 0 {
		trace.Editor("key_read", map[string]any{
			"error": "EOF",
		})
		return Key{}, io.EOF
	}

	// If it's an escape, try to read more for a sequence
	if r.buf[0] == 0x1b {
		key, err := r.readEscapeSequence()
		trace.Editor("key_read", map[string]any{
			"source": "escape_seq",
			"raw":    r.buf[:1],
			"parsed": keyString(key),
		})
		return key, err
	}

	key := ParseKey(r.buf[:1])
	trace.Editor("key_read", map[string]any{
		"source": "direct",
		"raw":    r.buf[:1],
		"parsed": keyString(key),
	})
	return key, nil
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

func (r *InputReader) readEscapeSequence() (Key, error) {
	// Try to read more bytes for escape sequence
	total := 1

	// Use timeout for first byte after ESC to detect standalone ESC
	firstRead := true

	for total < len(r.buf) {
		var n int
		var err error
		var timedOut bool

		if firstRead {
			// Use timeout to detect standalone ESC vs escape sequence
			n, err, timedOut = r.readWithTimeout(r.buf[total:total+1], r.escTimeout)
			firstRead = false
			if timedOut {
				// Timeout = standalone ESC key
				return Key{Special: KeyEscape}, nil
			}
		} else {
			n, err = r.in.Read(r.buf[total : total+1])
		}

		if err == io.EOF || n == 0 {
			break
		}
		if err != nil {
			break
		}

		// If second byte is a control character (not '[' or 'O'),
		// handle specially
		if total == 1 && r.buf[1] < 0x20 && r.buf[1] != '[' {
			// ESC + Enter (CR or LF) = Alt+Enter (used for multiline)
			// Some terminals send this for Shift+Enter.
			// Only treat as Alt+Enter if we're reading from a real terminal
			// (which supports deadlines). Otherwise treat as separate keys.
			if (r.buf[1] == '\r' || r.buf[1] == '\n') && r.supportsDeadlines() {
				return Key{Special: KeyEnter, Alt: true}, nil
			}
			// Other control chars (or non-terminal): treat as bare ESC and save for next read
			r.pending = r.buf[1]
			r.hasPending = true
			return Key{Special: KeyEscape}, nil
		}

		total++

		// Check if we have a complete sequence
		if isCompleteSequence(r.buf[:total]) {
			break
		}
	}

	// Check if this is a bracketed paste start sequence
	if isPasteStart(r.buf[:total]) {
		return r.readPasteContent()
	}

	return ParseKey(r.buf[:total]), nil
}

// readPasteContent reads all content until the paste end marker.
// Content is truncated to maxPasteSize to prevent memory exhaustion.
func (r *InputReader) readPasteContent() (Key, error) {
	r.pasteBuffer = r.pasteBuffer[:0] // Reset buffer

	// We keep 6 extra bytes beyond maxPasteSize for end marker detection
	maxSize := safeUintToInt(r.maxPasteSize)
	bufferLimit := maxSize + 6

	// Read until we see the paste end sequence \x1b[201~
	var singleByte [1]byte
	for {
		n, err := r.in.Read(singleByte[:])
		if err != nil {
			// Return whatever we have on error
			return Key{Special: KeyPaste, PasteText: string(r.pasteBuffer)}, err
		}
		if n == 0 {
			continue
		}

		// Append byte, but maintain sliding window when at limit
		if len(r.pasteBuffer) < bufferLimit {
			r.pasteBuffer = append(r.pasteBuffer, singleByte[0])
		} else {
			// Sliding window: shift left and add new byte
			copy(r.pasteBuffer, r.pasteBuffer[1:])
			r.pasteBuffer[bufferLimit-1] = singleByte[0]
		}

		// Check if we've reached the end marker
		if isPasteEnd(r.pasteBuffer) {
			// Remove the end marker from the content
			contentLen := len(r.pasteBuffer) - 6
			if contentLen > maxSize {
				contentLen = maxSize
			}
			content := r.pasteBuffer[:contentLen]
			return Key{Special: KeyPaste, PasteText: string(content)}, nil
		}
	}
}

// isCompleteSequence checks if we have a complete escape sequence.
func isCompleteSequence(b []byte) bool {
	if len(b) < 2 {
		return false
	}
	if b[1] != '[' {
		return true // Alt+char
	}

	// CSI sequence ends with a letter
	last := b[len(b)-1]
	return (last >= 'A' && last <= 'Z') || (last >= 'a' && last <= 'z') || last == '~'
}

// isPasteStart checks if the sequence is the bracketed paste start marker.
func isPasteStart(b []byte) bool {
	// \x1b[200~
	return len(b) == 6 && b[0] == 0x1b && b[1] == '[' && b[2] == '2' && b[3] == '0' && b[4] == '0' && b[5] == '~'
}

// isPasteEnd checks if the buffer ends with the bracketed paste end marker.
func isPasteEnd(b []byte) bool {
	// \x1b[201~
	if len(b) < 6 {
		return false
	}
	end := b[len(b)-6:]
	return end[0] == 0x1b && end[1] == '[' && end[2] == '2' && end[3] == '0' && end[4] == '1' && end[5] == '~'
}
