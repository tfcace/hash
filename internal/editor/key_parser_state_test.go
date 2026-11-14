package editor

import (
	"bytes"
	"os"
	"testing"
	"time"
)

// Sequences the editor has no binding for parse to the zero Key (discarded),
// never to Escape: Escape switches insert to normal and closes menus.
func TestParseKey_UnknownCSIIsDiscarded(t *testing.T) {
	for _, seq := range []string{
		"\x1b[I",        // focus in
		"\x1b[O",        // focus out
		"\x1b[57399;3u", // kitty functional key (private use area) + modifier
		"\x1b[12;5x",    // unknown final byte
		"\x1b[<0;10;20M",
	} {
		if got := ParseKey([]byte(seq)); got != (Key{}) {
			t.Errorf("ParseKey(%q) = %+v, want the zero (discarded) Key", seq, got)
		}
	}
}

func TestParseKey_ShiftTabCSIZ(t *testing.T) {
	if got, want := ParseKey([]byte("\x1b[Z")), (Key{Special: KeyTab, Shift: true}); got != want {
		t.Errorf("ParseKey(ESC[Z) = %+v, want %+v", got, want)
	}
}

// CSI u keys are matched on the whole codepoint, not a prefix of it.
func TestParseKey_CSIuMatchesWholeCodepoint(t *testing.T) {
	tests := []struct {
		seq  string
		want Key
	}{
		{"\x1b[97;5u", Key{Rune: 'a', Ctrl: true}},
		{"\x1b[91u", Key{Rune: '['}},
		{"\x1b[133;5u", Key{Rune: 133, Ctrl: true}},
		{"\x1b[27u", Key{Special: KeyEscape}},
		{"\x1b[127u", Key{Special: KeyBackspace}},
		{"\x1b[13;2u", Key{Special: KeyEnter, Shift: true}},
		{"\x1b[9;5u", Key{Special: KeyTab, Ctrl: true}},
		{"\x1b[1;5C", Key{Special: KeyRight, Ctrl: true}},
		{"\x1b[1;69A", Key{Special: KeyUp, Ctrl: true}}, // two-digit modifier (Ctrl + Caps Lock)
	}
	for _, tt := range tests {
		if got := ParseKey([]byte(tt.seq)); got != tt.want {
			t.Errorf("ParseKey(%q) = %+v, want %+v", tt.seq, got, tt.want)
		}
	}
}

// Shift+Tab on a typed line must not leave insert mode.
func TestEditor_ShiftTabKeepsInsertMode(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "ls")
	press(t, ed, ParseKey([]byte("\x1b[Z")))
	if _, ok := ed.mode.(*InsertMode); !ok {
		t.Fatalf("mode = %s, want insert", ed.mode.Name())
	}
}

// Alt plus a multibyte character arrives as one key.
func TestInputReader_AltMultibyteRune(t *testing.T) {
	r := NewInputReader(bytes.NewReader([]byte("\x1bé")))
	key, err := r.ReadKey()
	if err != nil {
		t.Fatal(err)
	}
	if want := (Key{Rune: 'é', Alt: true}); key != want {
		t.Fatalf("key = %+v, want %+v", key, want)
	}
}

// The reader skips discarded sequences instead of handing the editor a
// zero Key.
func TestInputReader_SkipsDiscardedSequences(t *testing.T) {
	r := NewInputReader(bytes.NewReader([]byte("\x1b[I\x1b[?2027;1$yx")))
	key, err := r.ReadKey()
	if err != nil {
		t.Fatal(err)
	}
	if key != (Key{Rune: 'x'}) {
		t.Fatalf("key = %+v, want the x after the discarded sequences", key)
	}
}

func pipeReader(t *testing.T) (*InputReader, *os.File) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pr.Close(); pw.Close() })
	return NewInputReader(pr), pw
}

type readResult struct {
	key Key
	err error
}

func readAsync(r *InputReader, done <-chan struct{}) <-chan readResult {
	ch := make(chan readResult, 1)
	go func() {
		k, err := r.ReadKeyInterruptible(done)
		ch <- readResult{k, err}
	}()
	return ch
}

// Alt+[ is ESC [ with nothing after it. The reader must not block on the
// rest of a CSI that never comes and swallow the next key.
func TestInputReader_AltBracketDoesNotSwallowNextKey(t *testing.T) {
	r, w := pipeReader(t)
	done := make(chan struct{})
	defer close(done)

	w.Write([]byte("\x1b["))
	got := readAsync(r, done)
	time.Sleep(150 * time.Millisecond)
	w.Write([]byte("x"))

	select {
	case res := <-got:
		if res.key != (Key{Rune: '[', Alt: true}) {
			t.Fatalf("first key = %+v, want Alt+[", res.key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader blocked")
	}
	res := <-readAsync(r, done)
	if res.key != (Key{Rune: 'x'}) {
		t.Fatalf("second key = %+v, want x", res.key)
	}
}

// A paste whose end marker never arrives must still let the reader stop
// when the editor is done: it is cut after pasteIdleTimeout and delivered as
// it stands (done is not honored mid-paste; see pasteState).
func TestInputReader_PasteWithoutEndStillEnds(t *testing.T) {
	r, w := pipeReader(t)
	done := make(chan struct{})

	w.Write([]byte("\x1b[200~abc"))
	got := readAsync(r, done)
	time.Sleep(100 * time.Millisecond)
	close(done)

	select {
	case res := <-got:
		if res.err != nil || res.key != (Key{Special: KeyPaste, PasteText: "abc", PasteCut: true}) {
			t.Fatalf("got %+v, %v; want the paste as it stood", res.key, res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("paste read blocked")
	}
}

// Shift+Tab steps backwards through an open menu.
func TestCompletionMenu_ShiftTabCyclesBackwards(t *testing.T) {
	e := newTestEditorForCompletion([]Completion{{Text: "a"}, {Text: "b"}, {Text: "c"}})
	e.handleCompletionKey(ParseKey([]byte("\x1b[Z")))
	if e.menu.index != 2 {
		t.Fatalf("index = %d, want 2 (wrapped backwards)", e.menu.index)
	}
}

// A byte that is not valid UTF-8 is no character: it must not be inserted as
// the Latin-1 rune of the same number.
func TestParseKey_InvalidUTF8IsDiscarded(t *testing.T) {
	for _, b := range [][]byte{{0xff}, {0xa9}, {0xc3, 0x28}} {
		if got := ParseKey(b); got != (Key{}) {
			t.Errorf("ParseKey(% x) = %+v, want the zero (discarded) Key", b, got)
		}
	}
}
