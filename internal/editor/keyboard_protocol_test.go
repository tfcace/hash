package editor

import (
	"context"
	"strings"
	"testing"
	"time"
)

// xterm's modifyOtherKeys (level 2) reports modified keys as
// CSI 27 ; mod ; codepoint ~. They used to be discarded, so Shift+Enter
// did nothing and, at level 2, Ctrl+C was lost.
func TestParseKey_ModifyOtherKeys(t *testing.T) {
	cases := map[string]Key{
		"\x1b[27;2;13~":  {Special: KeyEnter, Shift: true},
		"\x1b[27;3;13~":  {Special: KeyEnter, Alt: true},
		"\x1b[27;5;99~":  {Rune: 'c', Ctrl: true},
		"\x1b[27;5;100~": {Rune: 'd', Ctrl: true},
		"\x1b[27;3;120~": {Rune: 'x', Alt: true},
		"\x1b[27;2;9~":   {Special: KeyTab, Shift: true},
		"\x1b[27;5;127~": {Special: KeyBackspace, Ctrl: true},
		// kitty's disambiguate mode (flag 1)
		"\x1b[13;2u": {Special: KeyEnter, Shift: true},
		"\x1b[99;5u": {Rune: 'c', Ctrl: true},
		"\x1b[27u":   {Special: KeyEscape},
		// Control combos read as the legacy byte would: Ctrl+[ is Escape,
		// Ctrl+M Enter, whatever protocol reported them.
		"\x1b[91;5u":    {Special: KeyEscape},
		"\x1b[27;5;91~": {Special: KeyEscape},
		"\x1b[109;5u":   {Special: KeyEnter},
		"\x1b[27;6;67~": {Rune: 'C', Ctrl: true, Shift: true},
	}
	for in, want := range cases {
		if got := ParseKey([]byte(in)); got != want {
			t.Errorf("ParseKey(%q) = %+v, want %+v", in, got, want)
		}
	}
}

// The editor asks for the modes that make Shift+Enter distinct: kitty's
// disambiguate flag (CSI > 1 u) and xterm's modifyOtherKeys level 2
// (CSI > 4 ; 2 m), and puts both back on the way out. It sent
// CSI > 4 ; 1 u, which kitty reads as flag 4 (alternate keys) alone.
func TestRun_KeyboardModesSetAndRestored(t *testing.T) {
	_, slave := openPty(t)
	out := &screenBuf{}
	ed := New(Config{}, slave, out)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ed.Run(ctx) //nolint:errcheck // ends on the timeout
	s := out.String()
	on, off := strings.Index(s, "\x1b[>1u"), strings.LastIndex(s, "\x1b[<u")
	if on < 0 || !strings.Contains(s, "\x1b[>4;2m") {
		t.Fatalf("keyboard modes not enabled: %q", s)
	}
	if off < on || !strings.Contains(s[on:], "\x1b[>4m") {
		t.Errorf("keyboard modes not restored after use: %q", s)
	}
	if strings.Contains(s, "\x1b[>4;1u") {
		t.Errorf("still sends CSI > 4 ; 1 u: %q", s)
	}
}

// kitty and modifyOtherKeys report Super (Cmd), Hyper and Meta as modifier
// bits 8, 16 and 32. hash binds none of them, and reading such a chord as
// the bare key typed j for Cmd+J, ran the line for Ctrl+Cmd+J and ended the
// shell for Ctrl+Cmd+D on an empty line: the chord is discarded instead.
// The lock bits (Caps Lock 64, Num Lock 128) change no binding and stay
// ignored.
func TestParseKey_SuperHyperMetaChordsDiscarded(t *testing.T) {
	for _, in := range []string{
		"\x1b[106;9u",    // Cmd+J
		"\x1b[106;13u",   // Ctrl+Cmd+J
		"\x1b[100;13u",   // Ctrl+Cmd+D
		"\x1b[13;9u",     // Cmd+Enter
		"\x1b[106;17u",   // Hyper+J
		"\x1b[106;33u",   // Meta+J
		"\x1b[27;9;106~", // modifyOtherKeys Cmd+J
		"\x1b[1;9A",      // Cmd+Up
		"\x1b[3;9~",      // Cmd+Delete
		"\x1b[106;73u",   // Cmd+J with Caps Lock
	} {
		if got := ParseKey([]byte(in)); got != (Key{}) {
			t.Errorf("ParseKey(%q) = %+v, want the key discarded", in, got)
		}
	}
	locks := map[string]Key{
		"\x1b[106;65u":  {Rune: 'j'},             // Caps Lock
		"\x1b[106;129u": {Rune: 'j'},             // Num Lock
		"\x1b[99;69u":   {Rune: 'c', Ctrl: true}, // Ctrl+C with Caps Lock
		"\x1b[13;194u":  {Special: KeyEnter, Shift: true},
	}
	for in, want := range locks {
		if got := ParseKey([]byte(in)); got != want {
			t.Errorf("ParseKey(%q) = %+v, want %+v", in, got, want)
		}
	}
}

// The macOS text navigation chords terminals pass on for Cmd (Rex sends
// Cmd+Left as CSI 1;9 D) do what they do in a macOS text field and in
// Ghostty: Cmd+Left goes to the line's start, Cmd+Right to its end, and
// Cmd+Backspace kills to the line's start (Ctrl+U). They used to be
// discarded with every other Super chord, so they did nothing. A chord
// with Super and another modifier is still discarded.
func TestParseKey_CmdTextNavigation(t *testing.T) {
	for in, want := range map[string]Key{
		"\x1b[1;9D":   {Special: KeyHome},
		"\x1b[1;9C":   {Special: KeyEnd},
		"\x1b[127;9u": {Rune: 'u', Ctrl: true},
		"\x1b[1;73D":  {Special: KeyHome}, // with Caps Lock
		"\x1b[1;10D":  {},                 // Cmd+Shift+Left
		"\x1b[1;11C":  {},                 // Cmd+Alt+Right
		"\x1b[1;9A":   {},                 // Cmd+Up
	} {
		if got := ParseKey([]byte(in)); got != want {
			t.Errorf("ParseKey(%q) = %+v, want %+v", in, got, want)
		}
	}
}
