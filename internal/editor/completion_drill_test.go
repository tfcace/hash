package editor

import (
	"io"
	"strings"
	"testing"
)

func newTestDisplay() *Display {
	return &Display{
		out:   io.Discard,
		width: 80,
		view:  viewport{height: 24},
	}
}

func newTestEditorForCompletion(items []Completion) *Editor {
	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		mode:    NewInsertMode(),
	}
	e.menu = &completionMenu{items: items}
	return e
}

// TestDrillIntoDirectory verifies drilling into a directory replaces the buffer,
// pushes state onto the drill stack, and re-queries completions.
func TestDrillIntoDirectory(t *testing.T) {
	// Set up a completer that returns full-path items (like the real adapter)
	completeFunc := func(line string, pos int) []Completion {
		if pos >= 12 && line[:12] == "ls internal/" {
			return []Completion{
				{Text: "internal/editor/", Description: "directory"},
				{Text: "internal/parser/", Description: "directory"},
				{Text: "internal/shell/", Description: "directory"},
			}
		}
		return []Completion{
			{Text: "internal/", Description: "directory"},
			{Text: "cmd/", Description: "directory"},
			{Text: "go.mod", Description: "file"},
		}
	}

	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		config:  Config{CompleteOutcomeFunc: itemsOnly(completeFunc)},
	}
	// Simulate "ls int" with cursor at col 6, completion started at col 3
	e.state.Buffer = NewBufferFromString("ls int")
	e.state.Cursor.MoveTo(0, 6)
	e.menu = &completionMenu{}
	e.menu.from = 3
	e.menu.word = "int"
	e.menu.index = 0
	e.menu.items = []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "cmd/", Description: "directory"},
		{Text: "go.mod", Description: "file"},
	}

	// Drill into "internal/"
	e.drillIntoDirectory(Completion{Text: "internal/", Description: "directory"})

	// Verify buffer updated
	if got := e.state.Buffer.Content(); got != "ls internal/" {
		t.Errorf("buffer = %q, want %q", got, "ls internal/")
	}

	// Verify cursor position
	if e.state.Cursor.Pos.Col != 12 {
		t.Errorf("cursor col = %d, want 12", e.state.Cursor.Pos.Col)
	}

	// Verify drill stack has 1 entry
	if len(e.menu.drill) != 1 {
		t.Fatalf("drill stack length = %d, want 1", len(e.menu.drill))
	}
	if e.menu.drill[0].word != "int" {
		t.Errorf("drill stack prefix = %q, want %q", e.menu.drill[0].word, "int")
	}

	// Verify new completion items from re-query
	if len(e.menu.items) != 3 {
		t.Fatalf("completion items = %d, want 3", len(e.menu.items))
	}
	if e.menu.items[0].Text != "internal/editor/" {
		t.Errorf("first item = %q, want %q", e.menu.items[0].Text, "internal/editor/")
	}

	// Verify index is reset
	if e.menu.index != 0 {
		t.Errorf("index = %d, want 0", e.menu.index)
	}
}

// TestDrillUp verifies drilling up restores parent directory state.
func TestDrillUp(t *testing.T) {
	// Set up a completer that returns root-level items
	completeFunc := func(line string, pos int) []Completion {
		return []Completion{
			{Text: "internal/", Description: "directory"},
			{Text: "cmd/", Description: "directory"},
			{Text: "go.mod", Description: "file"},
		}
	}

	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		config:  Config{CompleteOutcomeFunc: itemsOnly(completeFunc)},
	}
	// Simulate being drilled into "internal/" with buffer "ls internal/"
	// menu.from points to word start (3), items have full paths
	e.state.Buffer = NewBufferFromString("ls internal/")
	e.state.Cursor.MoveTo(0, 12)
	e.menu = &completionMenu{}
	e.menu.from = 3 // Word start (where "internal/" begins)
	e.menu.word = "internal/"
	e.menu.index = 1
	e.menu.items = []Completion{
		{Text: "internal/editor/", Description: "directory"},
		{Text: "internal/parser/", Description: "directory"},
		{Text: "internal/shell/", Description: "directory"},
	}
	// The drill stack stores the parent state, including the col before drilling
	e.menu.drill = []completionLevel{
		{word: "int", index: 0, from: 3},
	}

	// Drill up
	e.drillUp()

	// Verify buffer restored to parent: the word is back to what it was
	// before the drill
	if got := e.state.Buffer.Content(); got != "ls int" {
		t.Errorf("buffer = %q, want %q", got, "ls int")
	}

	// Verify cursor position at the end of the restored word
	if e.state.Cursor.Pos.Col != 6 {
		t.Errorf("cursor col = %d, want 6", e.state.Cursor.Pos.Col)
	}

	// Verify drill stack is empty
	if len(e.menu.drill) != 0 {
		t.Errorf("drill stack length = %d, want 0", len(e.menu.drill))
	}

	// Verify menu.from restored
	if e.menu.from != 3 {
		t.Errorf("menu.from = %d, want 3", e.menu.from)
	}

	// Verify items re-queried for parent
	if len(e.menu.items) != 3 {
		t.Fatalf("completion items = %d, want 3", len(e.menu.items))
	}
	if e.menu.items[0].Text != "internal/" {
		t.Errorf("first item = %q, want %q", e.menu.items[0].Text, "internal/")
	}

	// Verify index and word restored
	if e.menu.index != 0 {
		t.Errorf("index = %d, want 0", e.menu.index)
	}
	if e.menu.word != "int" {
		t.Errorf("prefix = %q, want %q", e.menu.word, "int")
	}
}

// TestAutodrillSingleChild verifies that when a directory contains only
// one subdirectory, drilling auto-continues recursively.
func TestAutodrillSingleChild(t *testing.T) {
	// The completer returns single-child directories for two levels, then multiple items
	completeFunc := func(line string, pos int) []Completion {
		switch {
		case pos >= 15 && line[:15] == "ls src/main/go/":
			// Third level: multiple items, stop auto-drilling
			return []Completion{
				{Text: "src/main/go/handler.go", Description: "file"},
				{Text: "src/main/go/server.go", Description: "file"},
			}
		case pos >= 12 && line[:12] == "ls src/main/":
			// Second level: single subdirectory, should auto-drill
			return []Completion{
				{Text: "src/main/go/", Description: "directory"},
			}
		case pos >= 7 && line[:7] == "ls src/":
			// First level: single subdirectory, should auto-drill
			return []Completion{
				{Text: "src/main/", Description: "directory"},
			}
		default:
			return []Completion{
				{Text: "src/", Description: "directory"},
				{Text: "README.md", Description: "file"},
			}
		}
	}

	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		config:  Config{CompleteOutcomeFunc: itemsOnly(completeFunc)},
	}
	e.state.Buffer = NewBufferFromString("ls s")
	e.state.Cursor.MoveTo(0, 4)
	e.menu = &completionMenu{}
	e.menu.from = 3
	e.menu.word = "s"
	e.menu.index = 0
	e.menu.items = []Completion{
		{Text: "src/", Description: "directory"},
		{Text: "README.md", Description: "file"},
	}

	// Drill into "src/" — should auto-drill through "main/" and "go/"
	e.drillIntoDirectory(Completion{Text: "src/", Description: "directory"})

	// Should have auto-drilled through src/ -> main/ -> go/
	// Drill stack should have 3 entries (one for each level: original, src/, main/)
	if len(e.menu.drill) != 3 {
		t.Fatalf("drill stack length = %d, want 3 (auto-drilled through single-child dirs)", len(e.menu.drill))
	}

	// Buffer should show the fully drilled path
	if got := e.state.Buffer.Content(); got != "ls src/main/go/" {
		t.Errorf("buffer = %q, want %q", got, "ls src/main/go/")
	}

	// Final items should be the files in go/
	if len(e.menu.items) != 2 {
		t.Fatalf("completion items = %d, want 2", len(e.menu.items))
	}
	if e.menu.items[0].Text != "src/main/go/handler.go" {
		t.Errorf("first item = %q, want %q", e.menu.items[0].Text, "src/main/go/handler.go")
	}
}

// TestDrillIntoEmptyDirectory verifies that drilling into an empty directory
// dismisses the completion menu and pops the drill stack.
func TestDrillIntoEmptyDirectory(t *testing.T) {
	completeFunc := func(line string, pos int) []Completion {
		// Empty directory — no completions
		if pos >= 11 && line[:11] == "ls emptdir/" {
			return nil
		}
		return []Completion{
			{Text: "emptdir/", Description: "directory"},
			{Text: "file.txt", Description: "file"},
		}
	}

	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		config:  Config{CompleteOutcomeFunc: itemsOnly(completeFunc)},
	}
	e.state.Buffer = NewBufferFromString("ls emp")
	e.state.Cursor.MoveTo(0, 6)
	e.menu = &completionMenu{}
	e.menu.from = 3
	e.menu.word = "emp"
	e.menu.index = 0
	e.menu.items = []Completion{
		{Text: "emptdir/", Description: "directory"},
		{Text: "file.txt", Description: "file"},
	}

	e.drillIntoDirectory(Completion{Text: "emptdir/", Description: "directory"})

	// Empty directory should dismiss completion
	if e.menu != nil {
		t.Error("expected completion to be dismissed for empty directory")
	}

	// Buffer should still show the directory path
	if got := e.state.Buffer.Content(); got != "ls emptdir/" {
		t.Errorf("buffer = %q, want %q", got, "ls emptdir/")
	}
}

// TestDrillUpEmptyStack verifies that drillUp is a no-op with empty stack.
func TestDrillUpEmptyStack(t *testing.T) {
	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
	}
	e.state.Buffer = NewBufferFromString("ls foo")
	e.state.Cursor.MoveTo(0, 6)

	// Should not panic
	e.drillUp()

	// Buffer unchanged
	if got := e.state.Buffer.Content(); got != "ls foo" {
		t.Errorf("buffer = %q, want %q", got, "ls foo")
	}
}

// TestSingleDirectoryCompletesInline verifies that a single directory match
// is inserted in place like a single file: no one-row menu to dismiss.
func TestSingleDirectoryCompletesInline(t *testing.T) {
	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		config: Config{
			CompleteOutcomeFunc: itemsOnly(func(line string, pos int) []Completion {
				return []Completion{
					{Text: "internal/", Description: "directory"},
				}
			}),
		},
	}
	e.state.Buffer = NewBufferFromString("ls int")
	e.state.Cursor.MoveTo(0, 6)

	e.triggerCompletion()

	if e.menu != nil {
		t.Error("a single directory match must not open a menu")
	}
	if got := e.state.Buffer.Content(); got != "ls internal/" {
		t.Errorf("buffer = %q, want %q", got, "ls internal/")
	}
}

// TestSingleFileAutoInserts verifies that a single file match is
// auto-inserted without showing the completion menu.
func TestSingleFileAutoInserts(t *testing.T) {
	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		config: Config{
			CompleteOutcomeFunc: itemsOnly(func(line string, pos int) []Completion {
				return []Completion{
					{Text: "go.mod", Description: "go file"},
				}
			}),
		},
	}
	e.state.Buffer = NewBufferFromString("ls go.m")
	e.state.Cursor.MoveTo(0, 7)

	e.triggerCompletion()

	if e.menu != nil {
		t.Error("expected completion menu to be dismissed for single file match")
	}
	if got := e.state.Buffer.Content(); got != "ls go.mod" {
		t.Errorf("buffer = %q, want %q", got, "ls go.mod")
	}
}

// TestHandleCompletionKeyTabCycles verifies that Tab cycles through items.
func TestHandleCompletionKeyTabCycles(t *testing.T) {
	e := newTestEditorForCompletion([]Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "cmd/", Description: "directory"},
		{Text: "go.mod", Description: "file"},
	})
	e.menu.index = 0

	e.handleCompletionKey(Key{Special: KeyTab})
	if e.menu.index != 1 {
		t.Errorf("expected index 1 after first Tab, got %d", e.menu.index)
	}
	e.handleCompletionKey(Key{Special: KeyTab})
	if e.menu.index != 2 {
		t.Errorf("expected index 2 after second Tab, got %d", e.menu.index)
	}
	e.handleCompletionKey(Key{Special: KeyTab})
	if e.menu.index != 0 {
		t.Errorf("expected index 0 after wrap, got %d", e.menu.index)
	}
	if e.menu == nil {
		t.Error("expected completion to remain active after Tab cycling")
	}
}

// TestHandleCompletionKeyEnterDirectory verifies that Enter on a directory accepts it.
func TestHandleCompletionKeyEnterDirectory(t *testing.T) {
	e := newTestEditorForCompletion([]Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "cmd/", Description: "directory"},
		{Text: "go.mod", Description: "file"},
	})
	e.state.Buffer = NewBufferFromString("ls int")
	e.state.Cursor.MoveTo(0, 6)
	e.menu.from = 3
	e.menu.word = "int"
	e.menu.index = 0 // "internal/"

	handled, _ := e.handleCompletionKey(Key{Special: KeyEnter})
	if !handled {
		t.Fatal("expected Enter to be handled")
	}

	if e.menu != nil {
		t.Error("expected completion to be dismissed after Enter on directory")
	}
	if got := e.state.Buffer.Content(); got != "ls internal/" {
		t.Errorf("buffer = %q, want %q", got, "ls internal/")
	}
}

// TestHandleCompletionKeyEnterFile verifies that Enter on a file accepts it.
func TestHandleCompletionKeyEnterFile(t *testing.T) {
	e := newTestEditorForCompletion([]Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "go.mod", Description: "file"},
	})
	e.state.Buffer = NewBufferFromString("ls go")
	e.state.Cursor.MoveTo(0, 5)
	e.menu.from = 3
	e.menu.word = "go"
	e.menu.index = 1 // "go.mod"

	handled, _ := e.handleCompletionKey(Key{Special: KeyEnter})
	if !handled {
		t.Fatal("expected Enter to be handled")
	}

	if e.menu != nil {
		t.Error("expected completion to be dismissed after accepting file")
	}
	if got := e.state.Buffer.Content(); got != "ls go.mod" {
		t.Errorf("buffer = %q, want %q", got, "ls go.mod")
	}
}

// TestHandleCompletionKeyTyping verifies that printable characters are passed
// through to the active editing mode instead of being trapped in a hidden menu
// filter.
// Typing while the menu is open narrows it like a search list: the character
// lands in the buffer and the re-query filters the candidates.
func TestHandleCompletionKeyTyping(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
		{Text: "init.go", Description: "file"},
		{Text: "README.md", Description: "file"},
	}
	e := newTestEditorForCompletion(items)
	e.config.CompleteOutcomeFunc = itemsOnly(func(line string, pos int) []Completion {
		var out []Completion
		for _, item := range items {
			if strings.HasPrefix(item.Text, line) {
				out = append(out, item)
			}
		}
		return out
	})

	handled, _ := e.handleCompletionKey(Key{Rune: 'i'})
	if !handled {
		t.Fatal("expected printable key to narrow the open menu")
	}
	if e.menu == nil {
		t.Fatal("expected the menu to stay open while narrowing")
	}
	if got := e.state.Buffer.Content(); got != "i" {
		t.Fatalf("buffer = %q, want %q", got, "i")
	}
	if len(e.menu.items) != 3 {
		t.Fatalf("items = %+v, want the three i-prefixed entries", e.menu.items)
	}

	// Narrowing to a single candidate keeps the menu open: the user is
	// mid-word, auto-inserting here would hijack their typing.
	for _, r := range "nit" {
		e.handleCompletionKey(Key{Rune: r})
	}
	if e.menu == nil || len(e.menu.items) != 1 || e.menu.items[0].Text != "init.go" {
		t.Fatalf("after narrowing: active=%v items=%+v, want open menu with init.go", e.menu != nil, e.menu.items)
	}

	// Backspace un-narrows.
	e.handleCompletionKey(Key{Special: KeyBackspace})
	e.handleCompletionKey(Key{Special: KeyBackspace})
	if got := e.state.Buffer.Content(); got != "in" {
		t.Fatalf("buffer after backspace = %q, want %q", got, "in")
	}
	if len(e.menu.items) != 3 {
		t.Fatalf("items after backspace = %+v, want the three i(n)-prefixed entries", e.menu.items)
	}
}

// TestHandleCompletionKeyNavigation verifies Up/Down cycle and wrap.
func TestHandleCompletionKeyNavigation(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
	}
	e := newTestEditorForCompletion(items)

	// Start at index 0 (internal/)
	if e.menu.index != 0 {
		t.Fatalf("expected initial index 0, got %d", e.menu.index)
	}

	// Down → index 1 (interface.go)
	e.handleCompletionKey(Key{Special: KeyDown})
	if e.menu.index != 1 {
		t.Errorf("after Down: index = %d, want 1", e.menu.index)
	}

	// Down → wraps to index 0 (internal/)
	e.handleCompletionKey(Key{Special: KeyDown})
	if e.menu.index != 0 {
		t.Errorf("after second Down: index = %d, want 0 (wrap)", e.menu.index)
	}

	// Up → wraps to index 1 (last item)
	e.handleCompletionKey(Key{Special: KeyUp})
	if e.menu.index != 1 {
		t.Errorf("after Up from 0: index = %d, want 1 (wrap)", e.menu.index)
	}

	// Up → back to index 0
	e.handleCompletionKey(Key{Special: KeyUp})
	if e.menu.index != 0 {
		t.Errorf("after second Up: index = %d, want 0", e.menu.index)
	}
}

// TestHandleCompletionKeyAccept verifies Enter and Right accept the selected item.
func TestHandleCompletionKeyAccept(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
		{Text: "init.go", Description: "file"},
		{Text: "README.md", Description: "file"},
	}

	t.Run("enter_accepts_filtered", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		e.menu.index = 1 // interface.go in filtered list

		handled, _ := e.handleCompletionKey(Key{Special: KeyEnter})
		if !handled {
			t.Fatal("expected Enter to be handled")
		}
		// After accepting, completion should be dismissed
		if e.menu != nil {
			t.Error("expected completion to be dismissed after Enter")
		}
	})

	t.Run("right_accepts_filtered_file", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		e.menu.index = 1 // interface.go in filtered list

		handled, _ := e.handleCompletionKey(Key{Special: KeyRight})
		if !handled {
			t.Fatal("expected Right to be handled")
		}
		if e.menu != nil {
			t.Error("expected completion to be dismissed after Right")
		}
	})
}

func TestHandleCompletionKeyRightDrillsIntoDirectory(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
	}
	e := newTestEditorForCompletion(items)
	e.state = NewEditorState()
	e.state.Buffer = NewBufferFromString("ls int")
	e.state.Cursor.MoveTo(0, 6)
	e.menu.from = 3
	e.menu.word = "int"
	e.menu.index = 0
	e.config.CompleteOutcomeFunc = itemsOnly(func(line string, pos int) []Completion {
		if line == "ls internal/" && pos == len(line) {
			return []Completion{
				{Text: "internal/editor/", Description: "directory"},
				{Text: "internal/shell/", Description: "directory"},
			}
		}
		return nil
	})

	handled, _ := e.handleCompletionKey(Key{Special: KeyRight})
	if !handled {
		t.Fatal("expected Right to be handled")
	}
	if e.menu == nil {
		t.Fatal("expected completion to stay active after drilling into a directory")
	}
	if got := e.state.Buffer.Content(); got != "ls internal/" {
		t.Fatalf("buffer = %q, want %q", got, "ls internal/")
	}
	if len(e.menu.items) != 2 || e.menu.items[0].Text != "internal/editor/" {
		t.Fatalf("completion items after drill = %#v", e.menu.items)
	}
	if len(e.menu.drill) != 1 {
		t.Fatalf("drill stack length = %d, want 1", len(e.menu.drill))
	}
}

func TestCompletionMenuTypingContinuesEditing(t *testing.T) {
	files := []Completion{
		{Text: "README.md", Description: "file"},
		{Text: "release-notes.md", Description: "file"},
	}
	e := newTestEditorForCompletion(files)
	e.state = NewEditorState()
	e.mode = NewInsertMode()
	e.ghost = NewGhostText()
	e.state.Buffer = NewBufferFromString("ls r")
	e.state.Cursor.MoveTo(0, 4)
	e.menu.from = 3
	e.menu.word = "r"
	e.config.CompleteOutcomeFunc = itemsOnly(func(line string, pos int) []Completion {
		word := strings.TrimPrefix(line, "ls ")
		var out []Completion
		for _, item := range files {
			if strings.HasPrefix(strings.ToLower(item.Text), strings.ToLower(word)) {
				out = append(out, item)
			}
		}
		return out
	})

	result, done := e.handleKeyEvent(Key{Rune: 'e'})
	if done {
		t.Fatalf("typing while completion is active should not finish editing: %#v", result)
	}
	if e.menu == nil {
		t.Fatal("typing while completion is active should narrow the menu, not dismiss it")
	}
	if got := e.state.Buffer.Content(); got != "ls re" {
		t.Fatalf("buffer = %q, want %q", got, "ls re")
	}
	if len(e.menu.items) != 2 {
		t.Fatalf("items = %+v, want both re-matching files (README case-insensitively)", e.menu.items)
	}

	// A space ends the word and falls back to normal editing.
	if _, done := e.handleKeyEvent(Key{Rune: ' '}); done {
		t.Fatal("space should not finish editing")
	}
	if e.menu != nil {
		t.Fatal("space should dismiss the menu")
	}
	if got := e.state.Buffer.Content(); got != "ls re " {
		t.Fatalf("buffer = %q, want %q", got, "ls re ")
	}
}

// TestHandleCompletionKeyDismiss verifies Escape and Backspace dismiss behavior.
func TestHandleCompletionKeyDismiss(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
	}

	t.Run("escape_dismisses", func(t *testing.T) {
		e := newTestEditorForCompletion(items)

		handled, _ := e.handleCompletionKey(Key{Special: KeyEscape})
		if !handled {
			t.Fatal("expected Escape to be handled")
		}
		if e.menu != nil {
			t.Error("expected completion to be dismissed after Escape")
		}
	})

	t.Run("backspace_at_word_start_dismisses", func(t *testing.T) {
		e := newTestEditorForCompletion(items)

		handled, _ := e.handleCompletionKey(Key{Special: KeyBackspace})
		if handled {
			t.Fatal("expected Backspace at the word start with no drill stack to return false")
		}
		if e.menu != nil {
			t.Error("expected completion to be dismissed")
		}
	})

	t.Run("backspace_on_drilled_word_drills_up", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		e.state = NewEditorState()
		e.state.Buffer = NewBufferFromString("ls src/main/")
		e.state.Cursor.MoveTo(0, 12)
		e.menu.from = 3 // word start; the drilled word is the whole path
		e.menu.word = "src/main/"
		e.config.CompleteOutcomeFunc = itemsOnly(func(line string, pos int) []Completion {
			return items
		})
		e.menu.drill = []completionLevel{
			{word: "src/", index: 0, from: 3},
		}

		handled, _ := e.handleCompletionKey(Key{Special: KeyBackspace})
		if !handled {
			t.Fatal("expected Backspace with drill stack to return true (drillUp stays in menu)")
		}
		if e.menu == nil {
			t.Fatal("expected completion to remain active after drillUp")
		}
		if got := e.state.Buffer.Content(); got != "ls src/" {
			t.Errorf("buffer = %q, want the parent level %q", got, "ls src/")
		}
		if len(e.menu.drill) != 0 {
			t.Errorf("expected empty drill stack after drillUp, got %d", len(e.menu.drill))
		}
	})

	t.Run("non_printable_dismisses", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		// Ctrl+A is non-printable
		handled, _ := e.handleCompletionKey(Key{Rune: 'a', Ctrl: true})
		if handled {
			t.Fatal("expected Ctrl+A to pass through (not handled)")
		}
		if e.menu != nil {
			t.Error("expected completion to be dismissed on non-printable key")
		}
	})
}
