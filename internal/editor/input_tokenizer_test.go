package editor

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// keysFrom reads every key in input from a pipe (a timed reader, like the
// terminal).
func keysFrom(t *testing.T, input string) []Key {
	t.Helper()
	r, w := pipeReader(t)
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	w.Close()
	var keys []Key
	for {
		k, err := r.ReadKey()
		if err != nil {
			return keys
		}
		keys = append(keys, k)
	}
}

// Typeahead reads as the same keys as input typed live: there is one
// grammar. A drain path used to parse with its own rules (ESC CR and ESC
// Ctrl+A came out as no-op keys) and a second ESC used to be swallowed into
// the first one's sequence.
func TestInputReader_OneGrammar(t *testing.T) {
	cases := []struct {
		in   string
		want []Key
	}{
		{"\x1b\r", []Key{{Special: KeyEnter, Alt: true}}},
		{"\x1b\x01", []Key{{Special: KeyEscape}, {Rune: 'a', Ctrl: true}}},
		{"\x1b\x1b[A", []Key{{Special: KeyEscape}, {Special: KeyUp}}},
		{"\x1b[A\x1b[B", []Key{{Special: KeyUp}, {Special: KeyDown}}},
		{"\x1bx", []Key{{Rune: 'x', Alt: true}}},
		{"\x1bé", []Key{{Rune: 'é', Alt: true}}},
		{"\x1b\x7f", []Key{{Special: KeyBackspace, Alt: true}}},
		{"\x1b[?2027;1$yab", []Key{{Rune: 'a'}, {Rune: 'b'}}},
		{"\x1bOC", []Key{{Special: KeyRight}}},
		{"ש\r", []Key{{Rune: 'ש'}, {Special: KeyEnter}}},
		{"\x1b[200~p\x1b[201~q", []Key{{Special: KeyPaste, PasteText: "p"}, {Rune: 'q'}}},
		// Late replies to terminal queries are discarded whole, not typed.
		{"\x1b]11;rgb:0000/0000/0000\x1b\\x", []Key{{Rune: 'x'}}},
		{"\x1b]0;title\x07x", []Key{{Rune: 'x'}}},
		{"\x1bP>|kitty(0.31)\x1b\\x", []Key{{Rune: 'x'}}},
		{"\x1b]", []Key{{Rune: ']', Alt: true}}},
		{"\x1bP", []Key{{Rune: 'P', Alt: true}}},
	}
	for _, c := range cases {
		if got := keysFrom(t, c.in); !keysEqual(got, c.want) {
			t.Errorf("%q: keys = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func keysEqual(a, b []Key) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A stray UTF-8 lead byte is dropped at the first byte that cannot continue
// it, and that byte is the next key: "\xe9" used to swallow the x after it.
func TestInputReader_StrayLeadByteKeepsNextKey(t *testing.T) {
	if got, want := keysFrom(t, "\xe9xy"), []Key{{Rune: 'x'}, {Rune: 'y'}}; !keysEqual(got, want) {
		t.Errorf("keys = %+v, want %+v", got, want)
	}
}

// A lone lead byte with nothing after it must not block the reader: the
// next key typed after a pause still arrives.
func TestInputReader_StrayLeadByteDoesNotBlock(t *testing.T) {
	r, w := pipeReader(t)
	done := make(chan struct{})
	defer close(done)

	w.Write([]byte("\xe9"))
	got := readAsync(r, done)
	time.Sleep(150 * time.Millisecond)
	w.Write([]byte("x"))

	select {
	case res := <-got:
		if res.key != (Key{Rune: 'x'}) {
			t.Fatalf("key = %+v, want x", res.key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader blocked on a stray lead byte")
	}
}

// A paste over the cap keeps its head (what the user sees first), not its
// tail, and the keys after the end marker still arrive.
func TestInputReader_OversizedPasteKeepsHead(t *testing.T) {
	body := "HEAD" + strings.Repeat("x", 5000) + "TAIL"
	r := NewInputReader(bytes.NewReader([]byte("\x1b[200~" + body + "\x1b[201~z")))
	r.SetMaxPasteSize(1000)
	k, err := r.ReadKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(k.PasteText) != 1000 || !strings.HasPrefix(k.PasteText, "HEAD") {
		t.Fatalf("paste = %d bytes starting %q, want the first 1000", len(k.PasteText), k.PasteText[:min(8, len(k.PasteText))])
	}
	if k, _ := r.ReadKey(); k != (Key{Rune: 'z'}) {
		t.Fatalf("key after the paste = %+v, want z", k)
	}
}

// Reading a paste is linear in its size. Keeping the tail by shifting the
// whole buffer for every byte over the cap was O(size * excess): this one
// (1MB cap, 16MB pasted) did not finish in minutes.
func TestInputReader_OversizedPasteIsLinear(t *testing.T) {
	const capSize = 1 << 20
	body := bytes.Repeat([]byte("y"), 16<<20)
	in := append(append([]byte("\x1b[200~"), body...), "\x1b[201~"...)
	r := NewInputReader(bytes.NewReader(in))
	r.SetMaxPasteSize(capSize)

	done := make(chan Key, 1)
	go func() {
		k, _ := r.ReadKey()
		done <- k
	}()
	select {
	case k := <-done:
		if len(k.PasteText) != capSize {
			t.Fatalf("paste = %d bytes, want %d", len(k.PasteText), capSize)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a 16MB paste over a 1MB cap did not finish in 20s")
	}
}

// The end marker is found wherever reads split it.
func TestInputReader_PasteEndSplitAcrossReads(t *testing.T) {
	r, w := pipeReader(t)
	done := make(chan struct{})
	defer close(done)
	got := readAsync(r, done)
	for _, part := range []string{"\x1b[200~ab", "c\x1b[2", "01", "~d"} {
		w.WriteString(part)
		time.Sleep(10 * time.Millisecond)
	}
	res := <-got
	if res.key != (Key{Special: KeyPaste, PasteText: "abc"}) {
		t.Fatalf("paste = %+v, want abc", res.key)
	}
	if res := <-readAsync(r, done); res.key != (Key{Rune: 'd'}) {
		t.Fatalf("next key = %+v, want d", res.key)
	}
}

// keysTyped reads the keys of parts written to a pipe with a pause
// between them, as a person types them quickly, and nothing more. Each
// part is written once the reader has taken the last one off the pipe,
// then gap later, and the escape window is long, so every pause falls
// inside it however loaded the machine is.
func keysTyped(t *testing.T, gap time.Duration, parts ...string) []Key {
	t.Helper()
	r, w := pipeReader(t)
	r.escTimeout = time.Minute
	go func() {
		for i, p := range parts {
			if i > 0 {
				waitDrained(r)
				time.Sleep(gap)
			}
			_, _ = w.WriteString(p)
		}
		_ = w.Close()
	}()
	var keys []Key
	for {
		k, err := r.ReadKey()
		if err != nil {
			return keys
		}
		keys = append(keys, k)
	}
}

// Esc then a quick O or [ and more keys is Esc, then those keys: they came
// after the ESC, not with it, as a terminal sends a sequence. They used to
// be read as an SS3 or CSI sequence nothing knows and discarded whole: vi's
// Esc O (open a line above) then text lost three keys. The same bytes in
// one write are a sequence from the terminal, discarded as before, and a
// sequence that is a key stays that key.
func TestInputReader_EscThenQuickKeysAreNoSequence(t *testing.T) {
	esc := Key{Special: KeyEscape}
	for _, tc := range []struct {
		parts []string
		want  []Key
	}{
		{[]string{"\x1b", "Oh"}, []Key{esc, {Rune: 'O'}, {Rune: 'h'}}},
		{[]string{"\x1b", "[x"}, []Key{esc, {Rune: '['}, {Rune: 'x'}}},
		{[]string{"\x1b", "O", "h"}, []Key{esc, {Rune: 'O'}, {Rune: 'h'}}},
		{[]string{"\x1b", "[1;9z"}, []Key{esc, {Rune: '['}, {Rune: '1'}, {Rune: ';'}, {Rune: '9'}, {Rune: 'z'}}},
		{[]string{"\x1bOh"}, nil},
		{[]string{"\x1b[x"}, nil},
		{[]string{"\x1b", "[A"}, []Key{{Special: KeyUp}}},
		{[]string{"\x1b", "OA"}, []Key{{Special: KeyUp}}},
	} {
		if got := keysTyped(t, 10*time.Millisecond, tc.parts...); !keysEqual(got, tc.want) {
			t.Errorf("%q: keys = %+v, want %+v", tc.parts, got, tc.want)
		}
	}
}

// waitDrained waits until the reader has read every byte written to its
// pipe so far.
func waitDrained(r *InputReader) {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if !hasDataAvailableWithTimeout(r.fd, 0) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// A terminal report (private CSI parameters: DA, DECRPM, mouse; a cursor
// position report) whose ESC came alone is still a report, discarded: a
// late reply split across writes used to be read as Esc and typed text.
func TestInputReader_ASplitTerminalReportIsNoKeys(t *testing.T) {
	for _, report := range []string{"[?1;2c", "[?2004;1$y", "[<0;10;5M", "[>1;10;0c", "[=1c", "[40;1R", "[1;12R"} {
		if got := keysTyped(t, 10*time.Millisecond, "\x1b", report); len(got) > 0 {
			t.Errorf("ESC, then %q: keys %+v, want none", report, got)
		}
	}
}
