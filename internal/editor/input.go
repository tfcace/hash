// internal/editor/input.go
package editor

import (
	"io"
	"os"
	"syscall"
	"time"
)

// InputReader reads keys from a terminal.
type InputReader struct {
	in           io.Reader
	buf          [64]byte // Buffer for escape sequences
	escTimeout   time.Duration
	pending      byte   // Byte to return on next read (0 = none)
	hasPending   bool
	pendingKeys  []Key  // Keys to return before reading new input
}

// NewInputReader creates a new input reader.
func NewInputReader(in io.Reader) *InputReader {
	return &InputReader{
		in:         in,
		escTimeout: 50 * time.Millisecond,
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

// readWithTimeout reads with a timeout if the reader supports it.
// Returns (n, err, timedOut).
func (r *InputReader) readWithTimeout(buf []byte, timeout time.Duration) (int, error, bool) {
	// Check if reader supports deadlines (e.g., *os.File for terminals)
	if dr, ok := r.in.(deadlineReader); ok {
		dr.SetReadDeadline(time.Now().Add(timeout))
		n, err := r.in.Read(buf)
		dr.SetReadDeadline(time.Time{}) // Clear deadline

		// Check for timeout
		if err != nil {
			if os.IsTimeout(err) {
				return 0, nil, true
			}
		}
		return n, err, false
	}

	// No deadline support (e.g., bytes.Reader in tests) - just read
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
	for {
		if !hasDataAvailable(fd) {
			break
		}

		n, err := r.in.Read(r.buf[:1])
		if err != nil || n == 0 {
			break
		}

		// Parse and queue the key
		if r.buf[0] == 0x1b {
			// Might be start of escape sequence - try to read more
			key := r.drainEscapeSequence(fd)
			r.pendingKeys = append(r.pendingKeys, key)
		} else {
			r.pendingKeys = append(r.pendingKeys, ParseKey(r.buf[:1]))
		}
	}
}

// hasDataAvailable checks if there's data ready to read on fd without blocking.
func hasDataAvailable(fd int) bool {
	var readSet syscall.FdSet
	readSet.Bits[fd/64] |= 1 << (uint(fd) % 64)

	// Zero timeout = poll (return immediately)
	tv := syscall.Timeval{Sec: 0, Usec: 0}

	err := syscall.Select(fd+1, &readSet, nil, nil, &tv)
	if err != nil {
		return false
	}

	// Check if fd is still set in readSet (meaning data is available)
	return (readSet.Bits[fd/64] & (1 << (uint(fd) % 64))) != 0
}

// drainEscapeSequence reads an escape sequence during drain.
func (r *InputReader) drainEscapeSequence(fd int) Key {
	total := 1 // Already have ESC in buf[0]

	for total < len(r.buf) {
		if !hasDataAvailable(fd) {
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
	// Return any keys that were drained during mode transition
	if len(r.pendingKeys) > 0 {
		key := r.pendingKeys[0]
		r.pendingKeys = r.pendingKeys[1:]
		return key, nil
	}

	// Check for pending byte from previous escape sequence handling
	if r.hasPending {
		r.buf[0] = r.pending
		r.hasPending = false
		return ParseKey(r.buf[:1]), nil
	}

	// Read first byte
	n, err := r.in.Read(r.buf[:1])
	if err != nil {
		return Key{}, err
	}
	if n == 0 {
		return Key{}, io.EOF
	}

	// If it's an escape, try to read more for a sequence
	if r.buf[0] == 0x1b {
		return r.readEscapeSequence()
	}

	return ParseKey(r.buf[:1]), nil
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

	return ParseKey(r.buf[:total]), nil
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
