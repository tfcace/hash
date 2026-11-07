package editor

import (
	"bytes"
	"strings"
	"testing"
)

// Left, Right, Backspace and Delete step over a whole grapheme cluster, as
// widths and vertical motion count them: a cursor inside "é" (e + U+0301)
// or a ZWJ emoji sits on no cell, and deleting half of one leaves a
// different character behind.
func TestEditor_MotionAndDeletionByCluster(t *testing.T) {
	for _, cluster := range []string{"é", "👩‍💻", "🇮🇱"} {
		line := "a" + cluster + "b"
		for _, mode := range []struct {
			name  string
			setup func(*Editor)
		}{
			{"insert", func(*Editor) {}},
			{"normal", func(e *Editor) { e.setMode(NewNormalMode()) }},
		} {
			ed := New(Config{}, strings.NewReader(""), &bytes.Buffer{})
			ed.SetInitialText(line)
			mode.setup(ed)
			ed.state.Cursor.Pos.Col = len(line) - 1 // before b
			ed.handleKeyEvent(Key{Special: KeyLeft})
			if got := ed.state.Cursor.Pos.Col; got != 1 {
				t.Errorf("%s %q: Left from before b lands at byte %d, want 1 (before the cluster)", mode.name, cluster, got)
			}
			ed.handleKeyEvent(Key{Special: KeyRight})
			if got := ed.state.Cursor.Pos.Col; got != len(line)-1 {
				t.Errorf("%s %q: Right from before the cluster lands at byte %d, want %d", mode.name, cluster, got, len(line)-1)
			}
		}

		ed := New(Config{}, strings.NewReader(""), &bytes.Buffer{})
		ed.SetInitialText(line)
		ed.state.Cursor.Pos.Col = len(line) - 1
		ed.handleKeyEvent(Key{Special: KeyBackspace})
		if got := ed.state.Buffer.Content(); got != "ab" {
			t.Errorf("%q: Backspace after the cluster leaves %q, want \"ab\"", cluster, got)
		}
		ed.SetInitialText(line)
		ed.state.Cursor.Pos.Col = 1
		ed.handleKeyEvent(Key{Special: KeyDelete})
		if got := ed.state.Buffer.Content(); got != "ab" {
			t.Errorf("%q: Delete on the cluster leaves %q, want \"ab\"", cluster, got)
		}
	}
}
