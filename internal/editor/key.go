// internal/editor/key.go
package editor

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

	// UTF-8 multi-byte (simplified)
	r := rune(b[0])
	return Key{Rune: r}
}

func parseEscapeSequence(b []byte) Key {
	if len(b) < 2 {
		return Key{Special: KeyEscape}
	}

	// Alt+char: ESC followed by char
	if len(b) == 2 && b[1] != '[' {
		return Key{Rune: rune(b[1]), Alt: true}
	}

	// CSI sequences: ESC [
	if b[1] != '[' {
		return Key{Special: KeyEscape}
	}

	// Simple arrow keys: ESC [ A/B/C/D
	if len(b) == 3 {
		switch b[2] {
		case 'A':
			return Key{Special: KeyUp}
		case 'B':
			return Key{Special: KeyDown}
		case 'C':
			return Key{Special: KeyRight}
		case 'D':
			return Key{Special: KeyLeft}
		case 'H':
			return Key{Special: KeyHome}
		case 'F':
			return Key{Special: KeyEnd}
		}
	}

	// Modified keys: ESC [ 1 ; <mod> <dir>
	// mod: 2=Shift, 3=Alt, 4=Shift+Alt, 5=Ctrl, etc.
	if len(b) >= 6 && b[2] == '1' && b[3] == ';' {
		mod := b[4] - '0'
		dir := b[5]

		key := Key{}
		if mod&0x01 != 0 { // Bit 0 = Shift (2, 4, 6, 8)
			key.Shift = true
		}
		if mod == 3 || mod == 4 || mod == 7 || mod == 8 {
			key.Alt = true
		}
		if mod >= 5 {
			key.Ctrl = true
		}

		switch dir {
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

	// Delete key: ESC [ 3 ~
	if len(b) >= 4 && b[2] == '3' && b[3] == '~' {
		return Key{Special: KeyDelete}
	}

	// CSI u encoding for Enter: ESC [ 13 u or ESC [ 13 ; <mod> u
	// mod: 2=Shift, 3=Alt, 5=Ctrl, etc.
	if len(b) >= 5 && b[2] == '1' && b[3] == '3' && b[len(b)-1] == 'u' {
		key := Key{Special: KeyEnter}
		// Parse modifier between semicolon and 'u'
		for i := 4; i < len(b)-1; i++ {
			if b[i] == ';' && i+1 < len(b)-1 {
				mod := b[i+1] - '0'
				if mod == 2 || mod == 4 || mod == 6 || mod == 8 {
					key.Shift = true
				}
				if mod == 3 || mod == 4 || mod == 7 || mod == 8 {
					key.Alt = true
				}
				if mod >= 5 {
					key.Ctrl = true
				}
				break
			}
		}
		return key
	}

	// CSI u encoding for Tab: ESC [ 9 u or ESC [ 9 ; <mod> u
	if len(b) >= 4 && b[2] == '9' && b[len(b)-1] == 'u' {
		key := Key{Special: KeyTab}
		// Parse modifier between semicolon and 'u'
		for i := 3; i < len(b)-1; i++ {
			if b[i] == ';' && i+1 < len(b)-1 {
				mod := b[i+1] - '0'
				if mod == 2 || mod == 4 || mod == 6 || mod == 8 {
					key.Shift = true
				}
				if mod == 3 || mod == 4 || mod == 7 || mod == 8 {
					key.Alt = true
				}
				if mod >= 5 {
					key.Ctrl = true
				}
				break
			}
		}
		return key
	}

	return Key{Special: KeyEscape}
}
