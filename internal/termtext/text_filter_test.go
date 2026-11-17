package termtext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// filtered is text put through a TextFilter in the chunks given.
func filtered(chunks ...string) string {
	var f TextFilter
	var out strings.Builder
	for _, c := range chunks {
		out.WriteString(f.Write(c))
	}
	return out.String()
}

func TestTextFilter_LeavesTextAsItIs(t *testing.T) {
	for _, s := range []string{"", "plain", "two\nlines\n", "a\ttab", "日本語 and émoji 🙂", "[31m is not a sequence without its escape"} {
		if got := filtered(s); got != s {
			t.Errorf("filtered(%q) = %q", s, got)
		}
	}
}

// A sequence is removed whole: none of its bytes is left behind as text.
func TestTextFilter_RemovesWholeEscapeSequences(t *testing.T) {
	for name, tt := range map[string]struct{ in, want string }{
		"color":                            {"\x1b[31mred\x1b[0m", "red"},
		"cursor up and erase":              {"FAILED\n\x1b[1A\x1b[2Kok\n", "FAILED\nok\n"},
		"title, BEL ended":                 {"a\x1b]0;all green\x07b", "ab"},
		"title, ST ended":                  {"a\x1b]0;all green\x1b\\b", "ab"},
		"clipboard write":                  {"a\x1b]52;c;aGk=\x07b", "ab"},
		"hyperlink":                        {"\x1b]8;;http://x\x07link\x1b]8;;\x07", "link"},
		"device control":                   {"a\x1bPq#0;2;0;0;0\x1b\\b", "ab"},
		"application command":              {"a\x1b_Gf=100;x\x1b\\b", "ab"},
		"charset choice":                   {"a\x1b(Bb", "ab"},
		"reset":                            {"a\x1bcb", "ab"},
		"escape before escape":             {"a\x1b\x1b[31mb", "ab"},
		"escape before a wide character":   {"a\x1bé", "aé"},
		"sequence cut by a wide character": {"a\x1b[1é", "aé"},
		"escape before a tab":              {"a\x1b\tb", "a\tb"},
	} {
		if got := filtered(tt.in); got != tt.want {
			t.Errorf("%s: filtered(%q) = %q, want %q", name, tt.in, got, tt.want)
		}
	}
}

// What is left is text: the line ends and tabs stay, every other control
// character goes, so nothing can move the cursor or ring the bell.
func TestTextFilter_DropsControlCharacters(t *testing.T) {
	for name, tt := range map[string]struct{ in, want string }{
		"carriage return rewrite": {"FAILED\rok    \n", "FAILEDok    \n"},
		"CRLF":                    {"a\r\nb", "a\nb"},
		"backspace":               {"ab\b\bxy", "abxy"},
		"bell and nul":            {"a\x07\x00b", "ab"},
		"delete":                  {"a\x7fb", "ab"},
		"C1 controls":             {"a\u009b31mb\u0085c", "a31mbc"},
		"invalid byte":            {"a\xffb", "a�b"},
	} {
		if got := filtered(tt.in); got != tt.want {
			t.Errorf("%s: filtered(%q) = %q, want %q", name, tt.in, got, tt.want)
		}
	}
}

// A sequence that never ends must not swallow the rest of the reply: it
// stops at the end of its line.
func TestTextFilter_AnUnendedSequenceStopsAtTheLineEnd(t *testing.T) {
	for name, tt := range map[string]struct{ in, want string }{
		"title":  {"a\x1b]0;never ends\nnext line\n", "a\nnext line\n"},
		"CSI":    {"a\x1b[12;\nnext\n", "a\nnext\n"},
		"escape": {"a\x1b\nnext\n", "a\nnext\n"},
	} {
		if got := filtered(tt.in); got != tt.want {
			t.Errorf("%s: filtered(%q) = %q, want %q", name, tt.in, got, tt.want)
		}
	}
}

// The reply arrives in chunks cut anywhere: inside a sequence, inside a
// character.
func TestTextFilter_GivesTheSameTextHoweverItIsChunked(t *testing.T) {
	src := "ok \x1b[1;31mred\x1b[0m 日本 \x1b]0;title\x07 é\u009b🙂\r\n\x1b]8;;u\x1b\\link\x1b(B end"
	want := filtered(src)
	if want != "ok red 日本  é🙂\nlink end" {
		t.Fatalf("filtered whole = %q", want)
	}
	for i := 0; i <= len(src); i++ {
		for j := i; j <= len(src); j++ {
			if got := filtered(src[:i], src[i:j], src[j:]); got != want {
				t.Fatalf("split at %d and %d: %q, want %q", i, j, got, want)
			}
		}
	}
}

// Whatever comes in, what goes out cannot drive the terminal, and chunking
// changes nothing.
func FuzzTextFilter(f *testing.F) {
	f.Add("plain text\n", 3)
	f.Add("a\x1b[31mred\x1b[0m\x1b]0;t\x07\r\n", 5)
	f.Add("\x1b\x1b]\x1b[\u009b\xc2", 1)
	f.Fuzz(func(t *testing.T, s string, cut int) {
		whole := filtered(s)
		for i := 0; i < len(whole); i++ {
			if c := whole[i]; (c < 0x20 && c != '\n' && c != '\t') || c == 0x7f {
				t.Fatalf("control byte %#x in %q from %q", c, whole, s)
			}
		}
		if !utf8.ValidString(whole) {
			t.Fatalf("invalid UTF-8 in %q from %q", whole, s)
		}
		for _, r := range whole {
			if r >= 0x80 && r <= 0x9f {
				t.Fatalf("C1 control %U in %q from %q", r, whole, s)
			}
		}
		if cut = min(max(cut, 0), len(s)); filtered(s[:cut], s[cut:]) != whole {
			t.Fatalf("split at %d: %q, whole %q, from %q", cut, filtered(s[:cut], s[cut:]), whole, s)
		}
	})
}
