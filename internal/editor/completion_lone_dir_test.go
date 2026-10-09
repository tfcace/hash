package editor

import (
	"io"
	"strings"
	"testing"
)

// A lone directory match completes in place, like a lone file: no one-row
// menu and no extra Enter. The next Tab lists the directory's children.
func TestTab_LoneDirectoryCompletesInline(t *testing.T) {
	complete := func(line string, pos int) []Completion {
		word := line[strings.LastIndexByte(line[:pos], ' ')+1 : pos]
		switch word {
		case "inte":
			return []Completion{{Text: "internal/", Description: "directory"}}
		case "internal/":
			return []Completion{{Text: "internal/agent/"}, {Text: "internal/shell/"}}
		}
		return nil
	}
	ed := New(Config{Keybindings: "emacs", CompleteFunc: complete}, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("ls inte")
	ed.state.Cursor.MoveTo(0, 7)

	ed.handleKeyEvent(Key{Special: KeyTab})

	if got := ed.state.Buffer.Content(); got != "ls internal/" {
		t.Fatalf("buffer = %q, want %q", got, "ls internal/")
	}
	if ed.completionActive {
		t.Fatal("a lone directory should complete inline, not open a one-row menu")
	}

	ed.handleKeyEvent(Key{Special: KeyTab})

	if !ed.completionActive || len(ed.completionItems) != 2 {
		t.Fatalf("second Tab should list the children: active=%v items=%d", ed.completionActive, len(ed.completionItems))
	}
	if got := ed.state.Buffer.Content(); got != "ls internal/" {
		t.Errorf("buffer = %q after listing children, want unchanged", got)
	}
}
