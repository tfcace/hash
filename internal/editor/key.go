package editor

import "unicode/utf8"

// KeyCode represents special keys.
type KeyCode int

const (
	KeyNone KeyCode = iota
	KeyEnter
	KeyTab
	KeyBackspace
	KeyDelete
	KeyEscape
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyPaste // Bracketed paste content
)

// Key represents a parsed keypress.
type Key struct {
	Rune      rune
	Special   KeyCode
	Ctrl      bool
	Alt       bool
	Shift     bool
	PasteText string // Content for KeyPaste (bracketed paste)
}

// ParseKey parses a byte sequence into a Key.
func ParseKey(b []byte) Key {
	if len(b) == 0 {
		return Key{}
	}

	// Single byte
	if len(b) == 1 {
		c := b[0]

		// Control characters
		if c < 32 {
			switch c {
			case '\r', '\n':
				return Key{Special: KeyEnter}
			case '\t':
				return Key{Special: KeyTab}
			case 0x1b:
				return Key{Special: KeyEscape}
			default:
				// Ctrl+letter: 0x01-0x1a maps to a-z
				if c >= 1 && c <= 26 {
					return Key{Rune: rune('a' + c - 1), Ctrl: true}
				}
			}
		}

		// Backspace (DEL)
		if c == 0x7f {
			return Key{Special: KeyBackspace}
		}

		// Regular character
		return Key{Rune: rune(c)}
	}

	// Escape sequences
	if b[0] == 0x1b {
		return parseEscapeSequence(b)
	}

	r, _ := utf8.DecodeRune(b)
	return Key{Rune: r}
}

//nolint:gocyclo // escape sequence parsing requires matching multiple terminal protocols
func parseEscapeSequence(b []byte) Key {
	if len(b) < 2 {
		return Key{Special: KeyEscape}
	}

	// Alt+char: ESC followed by char
	if len(b) == 2 && b[1] != '[' {
		if b[1] == 0x7f || b[1] == 0x08 {
			return Key{Special: KeyBackspace, Alt: true}
		}
		return Key{Rune: rune(b[1]), Alt: true}
	}

	// SS3 sequences: ESC O <code>, sent for arrows/Home/End by terminals
	// in application cursor keys mode (DECCKM)
	if b[1] == 'O' {
		if len(b) >= 3 {
			if key, ok := parseSimpleCSI(b[2]); ok {
				return key
			}
		}
		return Key{} // Unknown SS3 (e.g. F1-F4): discard, don't fake Escape
	}

	// CSI sequences: ESC [
	if b[1] != '[' {
		return Key{Special: KeyEscape}
	}

	// Terminal responses (private mode reports, device attributes, etc.)
	// These are CSI sequences starting with '?' — silently discard them.
	// Examples: DECRPM \x1b[?2027;1$y, DA \x1b[?62;4c
	if len(b) >= 3 && b[2] == '?' {
		return Key{} // No-op: discard terminal response
	}

	// vt220/xterm "tilde" keys: ESC [ <num> (; <mod>)? ~
	// Home/End/Delete/PageUp/PageDown act; Insert and F-keys are discarded.
	// Checked before other CSI forms so ESC[1;2~ is not misread as ESC[1;<mod><dir>.
	if len(b) >= 4 && b[len(b)-1] == '~' {
		return parseTildeKey(b[2 : len(b)-1])
	}

	// Simple arrow keys: ESC [ A/B/C/D
	if len(b) == 3 {
		if key, ok := parseSimpleCSI(b[2]); ok {
			return key
		}
	}

	// Modified keys: ESC [ 1 ; <mod> <dir>
	if len(b) >= 6 && b[2] == '1' && b[3] == ';' {
		return parseModifiedKey(b[4], b[5])
	}

	// CSI u encoding for Enter: ESC [ 13 u or ESC [ 13 ; <mod> u
	if len(b) >= 5 && b[2] == '1' && b[3] == '3' && b[len(b)-1] == 'u' {
		return parseCsiUKey(KeyEnter, b[4:len(b)-1])
	}

	// CSI u encoding for Tab: ESC [ 9 u or ESC [ 9 ; <mod> u
	if len(b) >= 4 && b[2] == '9' && b[len(b)-1] == 'u' {
		return parseCsiUKey(KeyTab, b[3:len(b)-1])
	}

	return Key{Special: KeyEscape}
}

// parseTildeKey parses vt220-style sequences: the bytes between "ESC[" and
// the final "~", i.e. <num> optionally followed by ";<mod>". Unknown numbers
// (Insert, function keys) return a zero Key so they are ignored instead of
// being mistaken for Escape, which would silently switch editor modes.
func parseTildeKey(inner []byte) Key {
	num := 0
	i := 0
	for ; i < len(inner) && inner[i] >= '0' && inner[i] <= '9'; i++ {
		num = num*10 + int(inner[i]-'0')
	}

	var key Key
	switch num {
	case 1, 7:
		key = Key{Special: KeyHome}
	case 4, 8:
		key = Key{Special: KeyEnd}
	case 3:
		key = Key{Special: KeyDelete}
	case 5:
		key = Key{Special: KeyPageUp}
	case 6:
		key = Key{Special: KeyPageDown}
	default:
		return Key{}
	}

	if i < len(inner) && inner[i] == ';' && i+1 < len(inner) {
		key = applyModifier(key, inner[i+1]-'0')
	}
	return key
}

// parseSimpleCSI parses simple CSI sequences (ESC [ X) for arrow/navigation keys.
func parseSimpleCSI(code byte) (Key, bool) {
	switch code {
	case 'A':
		return Key{Special: KeyUp}, true
	case 'B':
		return Key{Special: KeyDown}, true
	case 'C':
		return Key{Special: KeyRight}, true
	case 'D':
		return Key{Special: KeyLeft}, true
	case 'H':
		return Key{Special: KeyHome}, true
	case 'F':
		return Key{Special: KeyEnd}, true
	}
	return Key{}, false
}

// parseModifiedKey parses modified key sequences (ESC [ 1 ; <mod> <dir>).
// mod: 2=Shift, 3=Alt, 4=Shift+Alt, 5=Ctrl, 6=Ctrl+Shift, 7=Ctrl+Alt, 8=Ctrl+Alt+Shift
func parseModifiedKey(modByte, dirByte byte) Key {
	key := applyModifier(Key{}, modByte-'0')

	switch dirByte {
	case 'A':
		key.Special = KeyUp
	case 'B':
		key.Special = KeyDown
	case 'C':
		key.Special = KeyRight
	case 'D':
		key.Special = KeyLeft
	case 'H':
		key.Special = KeyHome
	case 'F':
		key.Special = KeyEnd
	}
	return key
}

// parseCsiUKey parses CSI u encoded keys (e.g., ESC [ 13 ; <mod> u for Enter).
func parseCsiUKey(special KeyCode, modBytes []byte) Key {
	key := Key{Special: special}
	for i := 0; i < len(modBytes); i++ {
		if modBytes[i] == ';' && i+1 < len(modBytes) {
			key = applyModifier(key, modBytes[i+1]-'0')
			break
		}
	}
	return key
}

// applyModifier applies modifier bits to a key.
// Terminal modifier encoding: value = 1 + (shift?1:0) + (alt?2:0) + (ctrl?4:0)
// So: 2=Shift, 3=Alt, 4=Shift+Alt, 5=Ctrl, 6=Ctrl+Shift, 7=Ctrl+Alt, 8=Ctrl+Alt+Shift
func applyModifier(key Key, mod byte) Key {
	// Subtract 1 to get the bitmask: Shift=bit0, Alt=bit1, Ctrl=bit2
	if mod >= 2 {
		bits := mod - 1
		key.Shift = bits&1 != 0
		key.Alt = bits&2 != 0
		key.Ctrl = bits&4 != 0
	}
	return key
}
