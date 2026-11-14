package termtext

import (
	"strings"
	"testing"
)

func TestInert(t *testing.T) {
	cases := map[string]string{
		"plain":              "plain",
		"a\x1b[2Jb":          "a^[[2Jb",
		"bell\x07":           "bell^G",
		"tab\there":          "tab    here",
		"del\x7f":            "del^?",
		"c1\u009b31m":        "c1�31m",
		"bad\xff":            "bad�",
		"日本":                 "日本",
		"line\nbreak":        "line^Jbreak",
		"osc\x1b]0;t\x07end": "osc^[]0;t^Gend",
	}
	for in, want := range cases {
		if got := Inert(in); got != want {
			t.Errorf("Inert(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOneLine(t *testing.T) {
	got := OneLine("for i in 1 2\r\ndo echo $i\ndone\x1b[2J")
	if strings.ContainsAny(got, "\r\n\x1b") {
		t.Fatalf("OneLine left a break or escape: %q", got)
	}
	if want := "for i in 1 2 ↵ do echo $i ↵ done^[[2J"; got != want {
		t.Errorf("OneLine = %q, want %q", got, want)
	}
}

func TestWidthCountsWhatIsDrawn(t *testing.T) {
	cases := map[string]int{
		"abc":  3,
		"\t":   TabWidth,
		"\x1b": 2, // ^[
		"日本":   4,
		"\U0001F468\u200D\U0001F469\u200D\U0001F467": 2,
	}
	for in, want := range cases {
		if got := Width(in); got != want {
			t.Errorf("Width(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	family := "\U0001F468\u200D\U0001F469\u200D\U0001F467"
	if got := Truncate(family+family+family+"abc", 7, "..."); got != family+family+"..." {
		t.Errorf("Truncate kept %q", got)
	}
	if got := Truncate("short", 10, "..."); got != "short" {
		t.Errorf("Truncate(short) = %q", got)
	}
	if got := Truncate("a\x1b[2Jbcdefgh", 6, "..."); got != "a^[..." {
		t.Errorf("Truncate measures the drawn text: %q", got)
	}
}

func TestPlain(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain\n", "plain\n"},
		{"a\r\nb\r\n", "a\nb\n"},
		{"\x1b[1;31mred\x1b[0m and \x1b[38;2;1;2;3mrgb\x1b[m", "red and rgb"},
		{"\x1b]0;title\x07x\x1b]8;;http://e\x1b\\link\x1b]8;;\x1b\\", "xlink"},
		{"\x1b(B\x1b7\x1b[2Ka\x1b8", "a"},
		{"10%\r55%\r100%\n", "100%\n"},
		{"abc\rX\n", "Xbc\n"},
		{"_\bu\tx\x07\x00", "u\tx"},
		{"日本\r語\n", "語本\n"},
		{"cut \x1b[3", "cut "},
		{"downloading 100%\r\x1b[Kdone\n", "done\n"},
		{"downloading\r\x1b[0Kdone", "done"},
		{"\x1b[2K\rfetching 3/10\x1b[2K\rok\n", "ok\n"},
		{"abcdef\x1b[2Kx", "      x"},
		{"abcdef\b\b\b\x1b[1K", "    ef"},
		{"日本語\rab\n", "ab本語\n"},
		{"日本語\ra\n", "a 本語\n"},
		{"ab\rx日\n", "x日\n"},
		{"日本\b\b\x1b[Kx\n", "日x\n"},
		{"e\u0301x\rE\n", "Ex\n"},
		{"0\x1b0\xae\xae[1K0", "0\ufffd\ufffd[1K0"},
		{"日\ra\n", "a\n"},       // the overwritten half is a blank cell, not text
		{"a日\b\x1b[K\n", "a\n"}, // erasing half of it erases all of it
	}
	for _, tt := range tests {
		if got := Plain(tt.in); got != tt.want {
			t.Errorf("Plain(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
