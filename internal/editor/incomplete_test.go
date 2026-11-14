package editor

import (
	"io"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// shellIncomplete is the shell's Incomplete hook in small: bash syntax the
// parser reports as unfinished, read with the newline its row ends with.
func shellIncomplete(text string) bool {
	_, err := syntax.NewParser().Parse(strings.NewReader(text+"\n"), "")
	return err != nil && syntax.IsIncomplete(err)
}

// typeInto feeds keys to ed as typed, text as one key a character, and
// returns the line the first Enter that submits ran, or ok false.
func typeInto(ed *Editor, keys ...any) (submitted string, ok bool) {
	for _, k := range keys {
		var ks []Key
		switch k := k.(type) {
		case string:
			for _, r := range k {
				ks = append(ks, Key{Rune: r})
			}
		case Key:
			ks = []Key{k}
		}
		for _, key := range ks {
			if res, done := ed.handleKeyEvent(key); done {
				return res.Text, res.Exit == ExitSubmit
			}
		}
	}
	return "", false
}

var (
	enter      = Key{Special: KeyEnter}
	shiftEnter = Key{Special: KeyEnter, Shift: true}
	altEnter   = Key{Special: KeyEnter, Alt: true}
)

// Enter on a line the shell would read as unfinished (a compound command
// still open, a heredoc not ended, a quote not closed) adds a line, as
// bash's PS2 and zsh's continuation do, instead of running a line that can
// only fail to parse. Enter on a finished line runs it.
func TestEnter_IncompleteLineContinues(t *testing.T) {
	tests := []struct {
		name string
		keys []any
		want string
	}{
		{"for loop over three lines",
			[]any{"for i in 1 2; do", enter, "echo $i", enter, "done", enter},
			"for i in 1 2; do\necho $i\ndone"},
		{"heredoc",
			[]any{"cat <<EOF", enter, "hi", enter, "EOF", enter},
			"cat <<EOF\nhi\nEOF"},
		{"quoted paragraph break",
			[]any{`printf '[%s]' "subject`, enter, enter, `body"`, enter},
			"printf '[%s]' \"subject\n\nbody\""},
		{"finished line runs", []any{"echo hi", enter}, "echo hi"},
		{"syntax error runs, to be reported", []any{"echo )", enter}, "echo )"},
	}
	for _, tt := range tests {
		ed := New(Config{Incomplete: shellIncomplete}, strings.NewReader(""), io.Discard)
		got, ok := typeInto(ed, tt.keys...)
		if !ok || got != tt.want {
			t.Errorf("%s: submitted %q (%v), want %q; buffer %q", tt.name, got, ok, tt.want, ed.state.Buffer.Content())
		}
	}
}

// Enter after 'for i in 1 2; do' adds a line and runs nothing.
func TestEnter_AfterDoAddsALine(t *testing.T) {
	ed := New(Config{Incomplete: shellIncomplete}, strings.NewReader(""), io.Discard)
	if got, ok := typeInto(ed, "for i in 1 2; do", enter); ok {
		t.Fatalf("Enter ran %q", got)
	}
	if got := ed.state.Buffer.Content(); got != "for i in 1 2; do\n" {
		t.Errorf("buffer %q, want a new line after do", got)
	}
	if ed.state.Cursor.Pos != (Position{Row: 1, Col: 0}) {
		t.Errorf("cursor %+v, want the start of the new line", ed.state.Cursor.Pos)
	}
}

// Without the hook (an agent prompt, a reply) nothing is incomplete and
// Enter runs the line.
func TestEnter_NoHookRuns(t *testing.T) {
	ed := New(Config{}, strings.NewReader(""), io.Discard)
	if got, ok := typeInto(ed, "for i in 1 2; do", enter); !ok || got != "for i in 1 2; do" {
		t.Errorf("submitted %q (%v), want the line run", got, ok)
	}
}

// Shift+Enter and Alt+Enter inside a quote, a heredoc or a compound insert
// a plain newline: a quoted " \" newline is a line continuation in double
// quotes, and turned "a<S-Enter><S-Enter>b" into "a  b". At a finished
// command they keep " \", which continues the command on the next line.
func TestShiftEnter_PlainNewlineWhenTheTextBeforeIsIncomplete(t *testing.T) {
	tests := []struct {
		name string
		keys []any
		want string
	}{
		{"quoted newlines survive",
			[]any{`printf '[%s]' "a`, shiftEnter, shiftEnter, `b"`, enter},
			"printf '[%s]' \"a\n\nb\""},
		{"alt+enter in a single quote",
			[]any{`echo 'a`, altEnter, `b'`, enter},
			"echo 'a\nb'"},
		{"compound",
			[]any{"if true; then", shiftEnter, "echo y", shiftEnter, "fi", enter},
			"if true; then\necho y\nfi"},
		{"heredoc body",
			[]any{"cat <<EOF", shiftEnter, "x", shiftEnter, "EOF", enter},
			"cat <<EOF\nx\nEOF"},
		{"finished command keeps the continuation",
			[]any{"ls -la", shiftEnter, "/tmp", enter},
			"ls -la \\\n/tmp"},
		{"a trailing backslash is not doubled",
			[]any{`ls \`, shiftEnter, "/tmp", enter},
			"ls \\\n/tmp"},
		// An agent request's prompt is prose: a newline in it is a newline.
		{"agent request",
			[]any{"?? explain", shiftEnter, "briefly", enter},
			"?? explain\nbriefly"},
		{"agent pipe request",
			[]any{"ls | ?? which", shiftEnter, "is big", enter},
			"ls | ?? which\nis big"},
	}
	for _, tt := range tests {
		ed := New(Config{Incomplete: shellIncomplete}, strings.NewReader(""), io.Discard)
		got, ok := typeInto(ed, tt.keys...)
		if !ok || got != tt.want {
			t.Errorf("%s: submitted %q (%v), want %q; buffer %q", tt.name, got, ok, tt.want, ed.state.Buffer.Content())
		}
	}
}

// In normal mode Enter on an unfinished line adds a line at its end and
// goes back to insert, to type it.
func TestEnter_NormalModeIncompleteAddsALine(t *testing.T) {
	ed := New(Config{Incomplete: shellIncomplete}, strings.NewReader(""), io.Discard)
	typeInto(ed, "for i in 1 2; do", Key{Special: KeyEscape})
	if ed.mode.Name() != "normal" {
		t.Fatalf("mode %q, want normal", ed.mode.Name())
	}
	if got, ok := typeInto(ed, enter); ok {
		t.Fatalf("Enter ran %q", got)
	}
	if got := ed.state.Buffer.Content(); got != "for i in 1 2; do\n" {
		t.Errorf("buffer %q, want a new line at the end", got)
	}
	if ed.mode.Name() != "insert" || ed.state.Cursor.Pos != (Position{Row: 1, Col: 0}) {
		t.Errorf("mode %q cursor %+v, want insert at the new line", ed.mode.Name(), ed.state.Cursor.Pos)
	}
}

// Enter on an unfinished buffer adds the next line at the buffer's end,
// wherever the cursor is, as bash and zsh do: it never splits the line at
// the cursor (`for i in 1; do` with the cursor after `for` used to become
// `for` and ` i in 1; do`).
func TestEnter_IncompleteAddsTheLineAtTheEnd(t *testing.T) {
	ed := New(Config{Incomplete: shellIncomplete}, strings.NewReader(""), io.Discard)
	ed.SetInitialText("for i in 1; do\necho $i")
	ed.state.Cursor.Pos = Position{Row: 0, Col: len("for")}
	if _, ok := typeInto(ed, enter); ok {
		t.Fatal("Enter ran an unfinished buffer")
	}
	if got, want := ed.state.Buffer.Content(), "for i in 1; do\necho $i\n"; got != want {
		t.Errorf("buffer %q, want %q", got, want)
	}
	if got, want := ed.state.Cursor.Pos, (Position{Row: 2, Col: 0}); got != want {
		t.Errorf("cursor at %v, want %v (the new line)", got, want)
	}
}
