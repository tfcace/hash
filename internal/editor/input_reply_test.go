package editor

import (
	"strings"
	"testing"
)

// The tokenizer's grammar for terminal reply strings and the keys they
// start like.
func TestInputReader_RepliesAndAltKeys(t *testing.T) {
	alt := func(r rune) Key { return Key{Rune: r, Alt: true} }
	typed := func(s string) []Key { return runes(s) }
	cat := func(ks ...[]Key) []Key {
		var out []Key
		for _, k := range ks {
			out = append(out, k...)
		}
		return out
	}
	enter := []Key{{Special: KeyEnter}}
	cases := []struct {
		name string
		in   string
		want []Key
	}{
		// A reply longer than maxSequence is discarded whole: the cap used
		// to end the key and type the rest of the reply as characters.
		{"long OSC 52 reply", "\x1b]52;c;" + strings.Repeat("A", 300) + "\x07x", typed("x")},
		{"long OSC reply ended by ST", "\x1b]11;" + strings.Repeat("9", 400) + "\x1b\\x", typed("x")},
		{"long CSI", "\x1b[" + strings.Repeat("1;", 200) + "1~x", typed("x")},
		// Alt+], Alt+P and Alt+_ typed fast before ordinary text are keys;
		// only a reply's shape starts a reply string.
		{"Alt+] then typing", "\x1b]ls -la\r", cat([]Key{alt(']')}, typed("ls -la"), enter)},
		{"Alt+P then typing", "\x1bPwd\r", cat([]Key{alt('P')}, typed("wd"), enter)},
		{"Alt+_ then typing", "\x1b_x", cat([]Key{alt('_')}, typed("x"))},
		{"OSC reply", "\x1b]4;1;rgb:ffff/0000/0000\x07x", typed("x")},
		{"DCS XTVERSION reply", "\x1bP>|kitty(0.31)\x1b\\x", typed("x")},
		{"DCS DECRQSS reply", "\x1bP1$r0m\x1b\\x", typed("x")},
		{"DCS DECRPTUI reply", "\x1bP!|00000000\x1b\\x", typed("x")},
		{"APC kitty graphics reply", "\x1b_Gi=1;OK\x1b\\x", typed("x")},
		// ESC then a byte no character starts with: the Escape is kept,
		// the stray byte dropped.
		{"ESC stray byte", "\x1b\x80a", []Key{{Special: KeyEscape}, {Rune: 'a'}}},
		{"ESC cut character", "\x1b\xc3a", []Key{{Special: KeyEscape}, {Rune: 'a'}}},
		// Option+Delete where Delete sends ^H.
		{"ESC ^H", "\x1b\x08", []Key{{Special: KeyBackspace, Alt: true}}},
		// U+FFFD is a character like any other.
		{"typed U+FFFD", "�a", typed("�a")},
		{"Alt+U+FFFD", "\x1b�", []Key{alt('�')}},
	}
	for _, c := range cases {
		if got := keysFrom(t, c.in); !keysEqual(got, c.want) {
			t.Errorf("%s: keys = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestParseKey_ReplacementCharacter(t *testing.T) {
	if got := ParseKey([]byte("�")); got != (Key{Rune: '�'}) {
		t.Errorf("ParseKey(U+FFFD) = %+v", got)
	}
	if got := ParseKey([]byte("\xff")); got != (Key{}) {
		t.Errorf("ParseKey(0xff) = %+v, want discarded", got)
	}
}
