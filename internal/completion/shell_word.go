package completion

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// shellWordAt extracts the shell token ending at pos. Whitespace inside quotes
// or escaped with a backslash remains part of the token.
func shellWordAt(line string, pos int) string {
	if pos > len(line) {
		pos = len(line)
	}

	start := 0
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < pos; {
		r, size := utf8.DecodeRuneInString(line[i:pos])
		if r == utf8.RuneError && size == 0 {
			break
		}

		switch {
		case escaped:
			escaped = false
		case r == '\\' && !inSingle:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case (r == ' ' || r == '\t') && !inSingle && !inDouble:
			start = i + size
		}

		i += size
	}

	return line[start:pos]
}

func shellUnescapeWord(word string) string {
	var b strings.Builder
	b.Grow(len(word))

	inSingle := false
	inDouble := false
	for i := 0; i < len(word); {
		r, size := utf8.DecodeRuneInString(word[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}

		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case r == '\\' && !inSingle:
			i += size
			if i >= len(word) {
				b.WriteRune(r)
				continue
			}
			next, nextSize := utf8.DecodeRuneInString(word[i:])
			if !inDouble || strings.ContainsRune("$`\"\\\n", next) {
				b.WriteRune(next)
				i += nextSize
				continue
			}
			b.WriteRune(r)
			continue
		default:
			b.WriteRune(r)
		}

		i += size
	}

	return b.String()
}

// EscapeShellWord returns text that can be inserted as one shell word.
// It favors backslash escaping because it composes well with interactive path
// completion and directory drilling.
func EscapeShellWord(word string) string {
	if word == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(word))
	for _, r := range word {
		if isShellWordSafeRune(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('\\')
		b.WriteRune(r)
	}
	return b.String()
}

func isShellWordSafeRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	return strings.ContainsRune("@%_+=:,./-~", r)
}
