package editor

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// ESC and a key in one write is an Alt chord: what a terminal that does
// not encode Alt (tmux with extended-keys off, Terminal.app with Option as
// Meta) sends for Alt+key. One that no mode binds does nothing: it is no
// Esc, so the reply box stays open and the key is not typed. It used to
// read as Esc then the key: Alt+q canceled the reply box and typed q at
// the next prompt. A quick Esc then a key, the ESC alone, is still Esc
// then the key (the reader splits it).
func TestEditor_AnUnboundAltChordIsIgnoredInTheReplyBox(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	input := NewInputReader(r)
	ed := New(Config{Decline: DeclineEraseRow, Input: input}, r, &bytes.Buffer{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typeQuickly(input, w, "reply text", "\x1bq", "\r")
	res, err := ed.Run(ctx)
	if err != nil || res.Exit != ExitSubmit || res.Text != "reply text" {
		t.Fatalf("Alt+q as ESC q: exit %v, err %v, text %q; want the reply submitted as typed", res.Exit, err, res.Text)
	}
}

// An unbound Alt chord at a ghost or a menu closes it, as any key the
// ghost or menu has no use for does, and types nothing.
func TestEditor_AnUnboundAltChordAtAGhostOrMenu(t *testing.T) {
	altQ := Key{Rune: 'q', Alt: true}

	ed := newKeyEditor(t, Config{}, "git ")
	ed.SetGhostText("status")
	press(t, ed, altQ)
	if ed.ghost.visible() || ed.state.Buffer.Content() != "git " {
		t.Errorf("ghost: Alt+q left ghost=%v, buffer %q; want the ghost gone and nothing typed", ed.ghost.visible(), ed.state.Buffer.Content())
	}

	ed = treeEditor(t, "cat ")
	press(t, ed, keyTab())
	if ed.menu == nil {
		t.Fatal("no menu")
	}
	press(t, ed, altQ)
	if ed.menu != nil || ed.state.Buffer.Content() != "cat " {
		t.Errorf("menu: Alt+q left menu open=%v, buffer %q; want the menu closed and nothing typed", ed.menu != nil, ed.state.Buffer.Content())
	}
	if ed.mode.Name() != "insert" {
		t.Errorf("menu: mode %q, want insert", ed.mode.Name())
	}
}

// Bound Alt keys keep their meaning there.
func TestEditor_BoundAltKeysInTheReplyBox(t *testing.T) {
	ed := newKeyEditor(t, Config{Decline: DeclineEraseRow}, "one two")
	if _, done := press(t, ed, Key{Rune: 'b', Alt: true}); done {
		t.Fatal("Alt+b ended the reply box")
	}
	if ed.state.Cursor.Pos.Col != 4 {
		t.Errorf("Alt+b: cursor at %d, want 4 (word back)", ed.state.Cursor.Pos.Col)
	}
	if _, done := press(t, ed, Key{Rune: 'f', Alt: true}); done || ed.state.Cursor.Pos.Col != 7 {
		t.Errorf("Alt+f: done=%v cursor %d, want 7 (word forward)", done, ed.state.Cursor.Pos.Col)
	}
	if _, done := press(t, ed, Key{Special: KeyBackspace, Alt: true}); done || ed.state.Buffer.Content() != "one " {
		t.Errorf("Alt+Backspace: done=%v buffer %q, want %q", done, ed.state.Buffer.Content(), "one ")
	}
	if _, done := press(t, ed, Key{Special: KeyEnter, Alt: true}); done || ed.state.Buffer.LineCount() != 2 {
		t.Errorf("Alt+Enter: done=%v lines %d, want a newline", done, ed.state.Buffer.LineCount())
	}
}

// An Alt key the terminal encodes in a CSI sequence (kitty's CSI u, xterm's
// modifyOtherKeys) is Alt held with the key: no Esc was typed. It used to
// read as Esc then the key, so Alt+. in the reply box canceled it and lost
// the reply.
func TestEditor_AnEncodedAltKeyIsNoEsc(t *testing.T) {
	for _, seq := range []string{"\x1b[46;3u", "\x1b[27;3;46~"} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		input := NewInputReader(r)
		ed := New(Config{Decline: DeclineEraseRow, Input: input}, r, &bytes.Buffer{})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if _, err := w.WriteString("reply text" + seq + "\r"); err != nil {
			t.Fatal(err)
		}
		res, err := ed.Run(ctx)
		cancel()
		_ = r.Close()
		_ = w.Close()
		if err != nil || res.Exit != ExitSubmit || res.Text != "reply text" {
			t.Errorf("%q in the reply box: exit %v, err %v, text %q; want the reply submitted", seq, res.Exit, err, res.Text)
		}
	}

	key := ParseKey([]byte("\x1b[46;3u"))
	ed := newKeyEditor(t, Config{}, "git ")
	ed.SetGhostText("status")
	press(t, ed, key)
	if ed.state.Buffer.Content() != "git " {
		t.Errorf("ghost: Alt+. left buffer %q; want nothing typed", ed.state.Buffer.Content())
	}
}

// An unbound Alt chord in insert mode does nothing: it does not leave
// insert mode, so its key is no normal-mode command. Alt+u, Alt+d, Alt+.
// and Alt+c (readline's upcase, kill-word, yank-last-arg, capitalize) sent
// as ESC and the key used to be Esc then the key: u undid the typed line,
// x deleted a character.
func TestEditor_AnUnboundAltChordStaysInInsertMode(t *testing.T) {
	for _, r := range []rune{'u', 'd', '.', 'c', 'x', 'k', '0', 'h', '$'} {
		ed := newKeyEditor(t, Config{}, "echo hi")
		press(t, ed, Key{Rune: r, Alt: true})
		if ed.mode.Name() != "insert" || ed.state.Buffer.Content() != "echo hi" || ed.state.Cursor.Pos.Col != len("echo hi") {
			t.Errorf("Alt+%c: mode %s, buffer %q, cursor %v; want it ignored", r, ed.mode.Name(), ed.state.Buffer.Content(), ed.state.Cursor.Pos)
		}
	}
}

// The same through the reader: a line, a pause, then a legacy Alt chord in
// one write, then more typing: the chord does nothing.
func TestEditor_ALegacyAltChordAfterAPause(t *testing.T) {
	for _, chord := range []string{"\x1bu", "\x1bd", "\x1b.", "\x1bc"} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		input := NewInputReader(r)
		ed := New(Config{Input: input}, r, &bytes.Buffer{})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		typeQuickly(input, w, "git commit -m wip", chord, "w", "\r")
		res, err := ed.Run(ctx)
		cancel()
		_ = r.Close()
		_ = w.Close()
		if err != nil || res.Exit != ExitSubmit || res.Text != "git commit -m wipw" {
			t.Errorf("%q: exit %v, err %v, line %q; want \"git commit -m wipw\"", chord, res.Exit, err, res.Text)
		}
	}
}

// Esc then Enter (or Backspace) typed quickly is Esc, then the key: the ESC
// came alone, its key's byte not yet there. They used to be joined into
// Alt+Enter (a newline, and no Esc for whoever gives it a job) or
// Alt+Backspace. The same bytes in one write are the terminal's Alt+Enter
// (some send it for Shift+Enter) and Alt+Backspace.
func TestInputReader_EscThenAQuickEnterIsTwoKeys(t *testing.T) {
	esc, enter, bs := Key{Special: KeyEscape}, Key{Special: KeyEnter}, Key{Special: KeyBackspace}
	ctrlH := Key{Rune: 'h', Ctrl: true} // ^H alone, Backspace on some terminals
	for second, want := range map[string]Key{"\r": enter, "\n": enter, "\x08": ctrlH, "\x7f": bs} {
		if got := keysTyped(t, 10*time.Millisecond, "\x1b", second); !keysEqual(got, []Key{esc, want}) {
			t.Errorf("Esc, then a quick %q: keys %+v, want Esc, %+v", second, got, want)
		}
	}
	for in, want := range map[string]Key{
		"\x1b\r":   {Special: KeyEnter, Alt: true},
		"\x1b\x7f": {Special: KeyBackspace, Alt: true},
	} {
		if got := keysFrom(t, in); !keysEqual(got, []Key{want}) {
			t.Errorf("%q in one write: keys %+v, want %+v", in, got, want)
		}
	}
}

// typeQuickly writes parts to w as quick typing: each once r has taken the
// last off the pipe, then 10ms later, inside the escape window (made long,
// so a loaded machine cannot stretch a pause past it).
func typeQuickly(r *InputReader, w *os.File, parts ...string) {
	r.escTimeout = time.Minute
	go func() {
		for i, p := range parts {
			if i > 0 {
				waitDrained(r)
				time.Sleep(10 * time.Millisecond)
			}
			_, _ = w.WriteString(p)
		}
	}()
}

// The reply box cancels on Esc then a quick Enter, as on a slow one. It
// used to take the pair as Alt+Enter: a newline in the reply.
func TestEditor_EscThenAQuickEnterCancelsTheReplyBox(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	input := NewInputReader(r)
	ed := New(Config{Decline: DeclineEraseRow, Input: input}, r, &bytes.Buffer{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typeQuickly(input, w, "reply text", "\x1b", "\r")
	res, err := ed.Run(ctx)
	if err != nil || res.Exit != ExitCanceled {
		t.Fatalf("Esc then a quick Enter: exit %v, err %v, lines %d; want the reply box canceled", res.Exit, err, ed.state.Buffer.LineCount())
	}
	if k, err := input.ReadKey(); err != nil || k != (Key{Special: KeyEnter}) {
		t.Errorf("the next reader gets %+v (%v), want Enter", k, err)
	}
}

// At an open menu, Esc then a quick Enter does what a slow Esc, Enter does:
// Esc closes the menu, Enter runs the line.
func TestEditor_EscThenAQuickEnterAtAMenu(t *testing.T) {
	quick := keysTyped(t, 10*time.Millisecond, "\x1b", "\r")
	for _, keys := range [][]Key{quick, {keyEsc(), {Special: KeyEnter}}} {
		ed := treeEditor(t, "cat ")
		press(t, ed, keyTab())
		if ed.menu == nil {
			t.Fatal("no menu")
		}
		res, done := press(t, ed, keys...)
		if !done || res.Exit != ExitSubmit || res.Text != "cat " {
			t.Errorf("menu, keys %+v: done %v, exit %v, text %q, buffer %q; want the menu closed and %q run", keys, done, res.Exit, res.Text, ed.state.Buffer.Content(), "cat ")
		}
	}
}

// An ESC that came alone is Esc, and whatever is typed after it is its own
// key, however quickly: a quick Esc then b leaves insert mode and moves
// back a word in normal mode, as a slow one does. It used to read as Alt+b
// (word back, still inserting): x then typed an x. Only ESC and the key in
// one write may be Alt+key.
func TestEditor_EscThenAQuickBoundKeyLeavesInsertMode(t *testing.T) {
	esc := Key{Special: KeyEscape}
	for _, second := range []string{"b", "f", "é", "["} {
		want := []Key{esc, {Rune: []rune(second)[0]}}
		if got := keysTyped(t, 10*time.Millisecond, "\x1b", second); !keysEqual(got, want) {
			t.Errorf("Esc, then a quick %q: keys %+v, want %+v", second, got, want)
		}
	}
	quick := keysTyped(t, 10*time.Millisecond, "\x1b", "b", "x")
	slow := []Key{esc, {Rune: 'b'}, {Rune: 'x'}}
	q, s := newKeyEditor(t, Config{}, "echo hi there"), newKeyEditor(t, Config{}, "echo hi there")
	press(t, q, quick...)
	press(t, s, slow...)
	if q.mode.Name() != s.mode.Name() || q.state.Buffer.Content() != s.state.Buffer.Content() || q.state.Cursor.Pos != s.state.Cursor.Pos {
		t.Errorf("quick Esc b x: mode %s, buffer %q, cursor %v; slow: mode %s, buffer %q, cursor %v",
			q.mode.Name(), q.state.Buffer.Content(), q.state.Cursor.Pos, s.mode.Name(), s.state.Buffer.Content(), s.state.Cursor.Pos)
	}
	if got := keysFrom(t, "\x1bb"); !keysEqual(got, []Key{{Rune: 'b', Alt: true}}) {
		t.Errorf("ESC b in one write: keys %+v, want Alt+b", got)
	}
}

// In normal mode, which binds no Alt+Backspace, ESC DEL in one write does
// nothing: the selection stays. It used to be Esc, which cleared it.
func TestEditor_AnUnboundAltChordInNormalMode(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "one two")
	press(t, ed, keyEsc(), Key{Rune: 'v'}, Key{Rune: 'h'})
	if !ed.state.Cursor.HasSelection() {
		t.Fatal("no selection")
	}
	press(t, ed, Key{Special: KeyBackspace, Alt: true}, Key{Rune: 'u', Alt: true})
	if !ed.state.Cursor.HasSelection() || ed.state.Buffer.Content() != "one two" || ed.mode.Name() != "normal" {
		t.Errorf("normal mode, ESC DEL then ESC u: selection %v, buffer %q, mode %s; want them ignored",
			ed.state.Cursor.HasSelection(), ed.state.Buffer.Content(), ed.mode.Name())
	}
}
