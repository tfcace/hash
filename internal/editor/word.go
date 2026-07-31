package editor

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// wordSeparators are the characters (besides whitespace) that end a word for
// word motions. Underscore is deliberately absent so snake_case identifiers
// move as one word. Deletion keys (Ctrl+W, Alt+Backspace) intentionally keep
// whitespace-delimited semantics instead of this set.
const wordSeparators = "`~!@#$%^&*()-=+[{]}\\|;:'\",.<>/?«»"

// isWordSeparator reports whether r ends a word for word motions.
func isWordSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(wordSeparators, r)
}

// nextWordStart returns the byte column of the next word start after col:
// past the current word segment, then past any separators.
func nextWordStart(line string, col int) int {
	if col < 0 {
		col = 0
	}
	for col < len(line) {
		r, size := utf8.DecodeRuneInString(line[col:])
		if isWordSeparator(r) {
			break
		}
		col += size
	}
	for col < len(line) {
		r, size := utf8.DecodeRuneInString(line[col:])
		if !isWordSeparator(r) {
			break
		}
		col += size
	}
	return col
}

// prevWordStart returns the byte column of the previous word start before col:
// past any separators before the cursor, then to the start of that word.
func prevWordStart(line string, col int) int {
	if col > len(line) {
		col = len(line)
	}
	for col > 0 {
		r, size := utf8.DecodeLastRuneInString(line[:col])
		if !isWordSeparator(r) {
			break
		}
		col -= size
	}
	for col > 0 {
		r, size := utf8.DecodeLastRuneInString(line[:col])
		if isWordSeparator(r) {
			break
		}
		col -= size
	}
	return col
}

// wordEnd returns the byte column of the last rune of the next word after
// col (vim/helix-style e: advances at least one rune first).
func wordEnd(line string, col int) int {
	if col < len(line) {
		_, size := utf8.DecodeRuneInString(line[col:])
		col += size
	}
	for col < len(line) {
		r, size := utf8.DecodeRuneInString(line[col:])
		if !isWordSeparator(r) {
			break
		}
		col += size
	}
	for col < len(line) {
		r, size := utf8.DecodeRuneInString(line[col:])
		if isWordSeparator(r) {
			break
		}
		col += size
	}
	if col > 0 {
		col = previousRuneBoundary(line, col)
	}
	return col
}
