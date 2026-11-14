package editor

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

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
	Rune    rune
	Special KeyCode
	Ctrl    bool
	// Alt: Alt came with the key, however the terminal sent it: encoded as
	// a modifier (Alt+Left, kitty's CSI u, modifyOtherKeys) or as ESC and
	// the key in one write (Alt+character, Alt+Backspace, ESC CR: Alt+Enter
	// or some terminals' Shift+Enter). An ESC that came alone is Escape,
	// and the key after it its own (the reader splits them).
	Alt       bool
	Shift     bool
	PasteText string // Content for KeyPaste (bracketed paste)
	PasteCut  bool   // KeyPaste: the paste was cut short (over the size limit, or it stalled); PasteText is its head
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

		// A lone byte above ASCII is not a character (stray or truncated
		// UTF-8): discard it rather than insert its Latin-1 namesake.
		if c >= utf8.RuneSelf {
			return Key{}
		}

		// Regular character
		return Key{Rune: rune(c)}
	}

	// Escape sequences
	if b[0] == 0x1b {
		return parseEscapeSequence(b)
	}

	r, size := utf8.DecodeRune(b)
	if !isOneRune(r, size, len(b)) {
		return Key{} // invalid UTF-8
	}
	return Key{Rune: r}
}

// parseEscapeSequence parses a complete sequence that starts with ESC.
// Anything it does not recognize parses to the zero Key, which the reader
// discards: guessing (Escape, say) would switch modes or close menus on a
// focus report or an unbound key.
func parseEscapeSequence(b []byte) Key {
	if len(b) < 2 {
		return Key{Special: KeyEscape}
	}
	switch b[1] {
	case '[':
		if len(b) == 2 {
			// Nothing followed ESC [ before the timeout: Alt+[.
			return Key{Rune: '[', Alt: true}
		}
		return parseCSI(b[2:])
	case 'O':
		// SS3 (ESC O <code>): arrows/Home/End in application cursor keys
		// mode (DECCKM). Unknown SS3 codes (F1-F4) are discarded.
		if len(b) == 2 {
			return Key{Rune: 'O', Alt: true}
		}
		if key, ok := parseSimpleCSI(b[2]); ok && len(b) == 3 {
			return key
		}
		return Key{}
	case 0x7f, 0x08:
		if len(b) == 2 {
			return Key{Special: KeyBackspace, Alt: true}
		}
	case '\r', '\n':
		if len(b) == 2 {
			return Key{Special: KeyEnter, Alt: true}
		}
	}
	// Alt+char: ESC followed by one (possibly multibyte) character.
	r, size := utf8.DecodeRune(b[1:])
	if !isOneRune(r, size, len(b)-1) {
		if b[1] >= utf8.RuneSelf {
			// A character cut short after ESC: the Escape still happened.
			return Key{Special: KeyEscape}
		}
		return Key{}
	}
	return Key{Rune: r, Alt: true}
}

// isOneRune reports whether decoding n bytes gave the rune r of size bytes,
// all of them: a typed U+FFFD is a character, invalid UTF-8 is not.
func isOneRune(r rune, size, n int) bool {
	return size == n && (r != utf8.RuneError || size > 1)
}

// parseCSI parses the bytes after "ESC [": parameters, then a final byte.
func parseCSI(body []byte) Key {
	if isReport(body) {
		return Key{}
	}
	final := body[len(body)-1]
	params := body[:len(body)-1]
	parse, known := csiFinals[final]
	nums, ok := csiParams(params)
	if !known || !ok {
		return Key{}
	}
	mod := 1
	if len(nums) >= 2 {
		mod = nums[1]
	}
	return asLegacyControl(applyModifier(parse(nums), mod))
}

// isReport reports whether the CSI sequence whose bytes after "ESC [" are
// body is a terminal's report, never a key: one with private parameters (a
// first byte from < to ?: DECRPM, DA, mouse), or a cursor position report
// (row;column R, the reply to ESC[6n).
func isReport(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if body[0] >= '<' && body[0] <= '?' {
		return true
	}
	row, col, ok := strings.Cut(string(body[:len(body)-1]), ";")
	return body[len(body)-1] == 'R' && ok && isDigits(row) && isDigits(col)
}

// isDigits reports whether s is one or more ASCII digits.
func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// asLegacyControl reads Ctrl plus a character the way its legacy control
// byte reads: Ctrl+[ is Escape and Ctrl+M Enter whether the terminal sent
// the byte or, with kitty's flags or modifyOtherKeys, a CSI sequence.
func asLegacyControl(k Key) Key {
	if !k.Ctrl || k.Alt || k.Shift || k.Special != KeyNone {
		return k
	}
	var b byte
	switch r := k.Rune; {
	case r >= 'a' && r <= 'z':
		b = byte(r - 'a' + 1)
	case r >= '[' && r <= '_':
		b = byte(r - '@')
	default:
		return k
	}
	if legacy := ParseKey([]byte{b}); legacy != (Key{}) {
		return legacy
	}
	return k
}

// csiFinals maps a CSI final byte to the key its parameters encode. Each
// returns the zero Key for parameter shapes it does not know.
var csiFinals = map[byte]func(nums []int) Key{
	'A': csiCursorKey(KeyUp),
	'B': csiCursorKey(KeyDown),
	'C': csiCursorKey(KeyRight),
	'D': csiCursorKey(KeyLeft),
	'H': csiCursorKey(KeyHome),
	'F': csiCursorKey(KeyEnd),
	// Back-tab: Shift+Tab
	'Z': func(nums []int) Key {
		if len(nums) == 0 {
			return Key{Special: KeyTab, Shift: true}
		}
		return Key{}
	},
	// vt220/xterm "tilde" keys: ESC [ <num> (; <mod>)? ~, and xterm's
	// modifyOtherKeys: ESC [ 27 ; <mod> ; <codepoint> ~
	'~': func(nums []int) Key {
		if len(nums) == 3 && nums[0] == 27 {
			return csiUKey(nums[2])
		}
		return csiNumberedKey(tildeKey)(nums)
	},
	// CSI u (fixterms/kitty): ESC [ <codepoint> (; <mod>)? u
	'u': csiNumberedKey(csiUKey),
}

// csiCursorKey handles ESC [ <dir> and ESC [ 1 ; <mod> <dir>.
func csiCursorKey(code KeyCode) func([]int) Key {
	return func(nums []int) Key {
		if len(nums) > 2 || (len(nums) > 0 && nums[0] != 1) {
			return Key{}
		}
		return Key{Special: code}
	}
}

// csiNumberedKey handles ESC [ <num> (; <mod>)? <final>.
func csiNumberedKey(lookup func(int) Key) func([]int) Key {
	return func(nums []int) Key {
		if len(nums) == 0 || len(nums) > 2 {
			return Key{}
		}
		return lookup(nums[0])
	}
}

// csiParams parses ";"-separated decimal parameters. Kitty sub-parameters
// (":"-separated) are reduced to their first value. An empty or non-numeric
// parameter fails the parse, so a malformed sequence is discarded instead of
// read as random modifiers.
func csiParams(params []byte) ([]int, bool) {
	if len(params) == 0 {
		return nil, true
	}
	var nums []int
	for _, field := range bytes.Split(params, []byte{';'}) {
		if i := bytes.IndexByte(field, ':'); i >= 0 {
			field = field[:i]
		}
		if len(field) == 0 || len(field) > 7 {
			return nil, false
		}
		n := 0
		for _, c := range field {
			if c < '0' || c > '9' {
				return nil, false
			}
			n = n*10 + int(c-'0')
		}
		nums = append(nums, n)
	}
	return nums, true
}

// tildeKey maps a vt220 key number. Insert and function keys have no binding
// and return the zero Key.
func tildeKey(num int) Key {
	switch num {
	case 1, 7:
		return Key{Special: KeyHome}
	case 4, 8:
		return Key{Special: KeyEnd}
	case 3:
		return Key{Special: KeyDelete}
	case 5:
		return Key{Special: KeyPageUp}
	case 6:
		return Key{Special: KeyPageDown}
	}
	return Key{}
}

// csiUKey maps a CSI u codepoint to a key.
func csiUKey(cp int) Key {
	switch cp {
	case 13:
		return Key{Special: KeyEnter}
	case 9:
		return Key{Special: KeyTab}
	case 27:
		return Key{Special: KeyEscape}
	case 127, 8:
		return Key{Special: KeyBackspace}
	}
	if cp < ' ' || cp > utf8.MaxRune || isPrivateUse(cp) {
		return Key{}
	}
	r := rune(cp) //nolint:gosec // bounded to [' ', utf8.MaxRune] just above
	if !utf8.ValidRune(r) {
		return Key{}
	}
	return Key{Rune: r}
}

// isPrivateUse reports codepoints in the Unicode private use area, where
// kitty encodes functional keys (keypad, media, lone modifiers).
func isPrivateUse(cp int) bool {
	return cp >= 0xE000 && cp <= 0xF8FF
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

// Modifier bits of a CSI key's modifier parameter (the parameter is the
// bits plus 1). Shift, Alt and Ctrl are read; Super (Cmd), Hyper and Meta
// bind nothing but Cmd's text navigation (superKey), so any other chord
// with one is discarded rather than read as the bare key; Caps Lock and
// Num Lock change no binding and are ignored.
const (
	modShift = 1 << iota
	modAlt
	modCtrl
	modSuper
	modHyper
	modMeta
)

// applyModifier applies a CSI modifier parameter to a key: 2 is Shift, 3
// Alt, 5 Ctrl, 9 Super, and so on (1 plus the bits).
func applyModifier(key Key, mod int) Key {
	if key == (Key{}) {
		return key // discarded keys stay discarded
	}
	if mod >= 2 {
		bits := mod - 1
		if bits&(modSuper|modHyper|modMeta) != 0 {
			return superKey(key, bits)
		}
		key.Shift = bits&modShift != 0
		key.Alt = bits&modAlt != 0
		key.Ctrl = bits&modCtrl != 0
	}
	return key
}

// superKey is the key a chord with Super, Hyper or Meta stands for: the
// macOS text navigation chords terminals pass on for Cmd, as macOS text
// fields and Ghostty take them (Cmd+Left the line's start, Cmd+Right its
// end, Cmd+Backspace kills to its start, as Ctrl+U), and nothing for every
// other chord.
func superKey(key Key, bits int) Key {
	if bits&(modShift|modAlt|modCtrl|modSuper|modHyper|modMeta) != modSuper {
		return Key{}
	}
	switch key {
	case Key{Special: KeyLeft}:
		return Key{Special: KeyHome}
	case Key{Special: KeyRight}:
		return Key{Special: KeyEnd}
	case Key{Special: KeyBackspace}:
		return Key{Rune: 'u', Ctrl: true}
	}
	return Key{}
}
