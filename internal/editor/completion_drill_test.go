package editor

import (
	"io"
	"testing"
)

// TestCompletionDrillStackPushPop verifies the drill state can be pushed and popped.
func TestCompletionDrillStackPushPop(t *testing.T) {
	e := &Editor{}

	// Initially empty
	if len(e.completionDrillStack) != 0 {
		t.Fatalf("expected empty drill stack, got %d", len(e.completionDrillStack))
	}

	// Push a state
	e.completionDrillStack = append(e.completionDrillStack, completionDrillState{
		prefix: "src/",
		filter: "ma",
		index:  2,
	})

	if len(e.completionDrillStack) != 1 {
		t.Fatalf("expected 1 entry after push, got %d", len(e.completionDrillStack))
	}

	// Push another state
	e.completionDrillStack = append(e.completionDrillStack, completionDrillState{
		prefix: "src/main/",
		filter: "",
		index:  0,
	})

	if len(e.completionDrillStack) != 2 {
		t.Fatalf("expected 2 entries after second push, got %d", len(e.completionDrillStack))
	}

	// Pop the top
	top := e.completionDrillStack[len(e.completionDrillStack)-1]
	e.completionDrillStack = e.completionDrillStack[:len(e.completionDrillStack)-1]

	if top.prefix != "src/main/" {
		t.Errorf("expected popped prefix %q, got %q", "src/main/", top.prefix)
	}
	if top.filter != "" {
		t.Errorf("expected popped filter %q, got %q", "", top.filter)
	}
	if top.index != 0 {
		t.Errorf("expected popped index 0, got %d", top.index)
	}

	// Pop the remaining entry
	top = e.completionDrillStack[len(e.completionDrillStack)-1]
	e.completionDrillStack = e.completionDrillStack[:len(e.completionDrillStack)-1]

	if top.prefix != "src/" {
		t.Errorf("expected popped prefix %q, got %q", "src/", top.prefix)
	}
	if top.filter != "ma" {
		t.Errorf("expected popped filter %q, got %q", "ma", top.filter)
	}
	if top.index != 2 {
		t.Errorf("expected popped index 2, got %d", top.index)
	}

	if len(e.completionDrillStack) != 0 {
		t.Fatalf("expected empty drill stack after popping all, got %d", len(e.completionDrillStack))
	}
}

// TestCompletionDrillStateFields verifies the drill state struct fields.
func TestCompletionDrillStateFields(t *testing.T) {
	state := completionDrillState{
		prefix: "internal/editor/",
		filter: "buf",
		index:  5,
	}

	if state.prefix != "internal/editor/" {
		t.Errorf("prefix = %q, want %q", state.prefix, "internal/editor/")
	}
	if state.filter != "buf" {
		t.Errorf("filter = %q, want %q", state.filter, "buf")
	}
	if state.index != 5 {
		t.Errorf("index = %d, want %d", state.index, 5)
	}
}

// TestFilterCompletionItems tests filtering with a prefix.
func TestFilterCompletionItems(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
		{Text: "init.go", Description: "file"},
		{Text: "README.md", Description: "file"},
	}

	t.Run("filter_int_matches_two", func(t *testing.T) {
		result := filterCompletionItems(items, "int")
		if len(result) != 2 {
			t.Fatalf("expected 2 matches for 'int', got %d", len(result))
		}
		expected := []string{"internal/", "interface.go"}
		for i, want := range expected {
			if result[i].Text != want {
				t.Errorf("result[%d].Text = %q, want %q", i, result[i].Text, want)
			}
		}
	})

	t.Run("filter_in_matches_three", func(t *testing.T) {
		result := filterCompletionItems(items, "in")
		if len(result) != 3 {
			t.Fatalf("expected 3 matches for 'in', got %d", len(result))
		}
		expected := []string{"internal/", "interface.go", "init.go"}
		for i, want := range expected {
			if result[i].Text != want {
				t.Errorf("result[%d].Text = %q, want %q", i, result[i].Text, want)
			}
		}
	})

	t.Run("filter_read_case_insensitive", func(t *testing.T) {
		result := filterCompletionItems(items, "read")
		if len(result) != 1 {
			t.Fatalf("expected 1 match for 'read', got %d", len(result))
		}
		if result[0].Text != "README.md" {
			t.Errorf("result[0].Text = %q, want %q", result[0].Text, "README.md")
		}
	})

	t.Run("empty_filter_returns_all", func(t *testing.T) {
		result := filterCompletionItems(items, "")
		if len(result) != len(items) {
			t.Fatalf("expected %d items for empty filter, got %d", len(items), len(result))
		}
		for i, item := range items {
			if result[i].Text != item.Text {
				t.Errorf("result[%d].Text = %q, want %q", i, result[i].Text, item.Text)
			}
		}
	})

	t.Run("no_match_returns_nil", func(t *testing.T) {
		result := filterCompletionItems(items, "xyz")
		if len(result) != 0 {
			t.Fatalf("expected 0 matches for 'xyz', got %d", len(result))
		}
	})
}

// TestFilteredCompletionItems tests the Editor method that combines items with filter.
func TestFilteredCompletionItems(t *testing.T) {
	e := &Editor{}
	e.completionItems = []Completion{
		{Text: "alpha", Description: "first"},
		{Text: "beta", Description: "second"},
		{Text: "alpine", Description: "third"},
	}

	t.Run("no_filter", func(t *testing.T) {
		e.completionFilter = ""
		result := e.filteredCompletionItems()
		if len(result) != 3 {
			t.Fatalf("expected 3 items, got %d", len(result))
		}
	})

	t.Run("filter_al", func(t *testing.T) {
		e.completionFilter = "al"
		result := e.filteredCompletionItems()
		if len(result) != 2 {
			t.Fatalf("expected 2 items for 'al', got %d", len(result))
		}
		if result[0].Text != "alpha" {
			t.Errorf("result[0].Text = %q, want %q", result[0].Text, "alpha")
		}
		if result[1].Text != "alpine" {
			t.Errorf("result[1].Text = %q, want %q", result[1].Text, "alpine")
		}
	})

	t.Run("case_insensitive", func(t *testing.T) {
		e.completionFilter = "AL"
		result := e.filteredCompletionItems()
		if len(result) != 2 {
			t.Fatalf("expected 2 items for 'AL', got %d", len(result))
		}
	})

	t.Run("filter_b", func(t *testing.T) {
		e.completionFilter = "b"
		result := e.filteredCompletionItems()
		if len(result) != 1 {
			t.Fatalf("expected 1 item for 'b', got %d", len(result))
		}
		if result[0].Text != "beta" {
			t.Errorf("result[0].Text = %q, want %q", result[0].Text, "beta")
		}
	})
}

func newTestDisplay() *Display {
	return &Display{
		out:    io.Discard,
		width:  80,
		height: 24,
	}
}

func newTestEditorForCompletion(items []Completion) *Editor {
	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
	}
	e.completionActive = true
	e.completionItems = items
	e.completionIndex = 0
	e.completionFilter = ""
	return e
}

// TestDrillIntoDirectory verifies drilling into a directory replaces the buffer,
// pushes state onto the drill stack, and re-queries completions.
func TestDrillIntoDirectory(t *testing.T) {
	// Set up a CompleteFunc that returns full-path items (like the real adapter)
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
		config:  Config{CompleteFunc: completeFunc},
	}
	// Simulate "ls int" with cursor at col 6, completion started at col 3
	e.state.Buffer = NewBufferFromString("ls int")
	e.state.Cursor.MoveTo(0, 6)
	e.completionActive = true
	e.completionCol = 3
	e.completionPrefix = "int"
	e.completionFilter = ""
	e.completionIndex = 0
	e.completionItems = []Completion{
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
	if len(e.completionDrillStack) != 1 {
		t.Fatalf("drill stack length = %d, want 1", len(e.completionDrillStack))
	}
	if e.completionDrillStack[0].prefix != "int" {
		t.Errorf("drill stack prefix = %q, want %q", e.completionDrillStack[0].prefix, "int")
	}

	// Verify new completion items from re-query
	if len(e.completionItems) != 3 {
		t.Fatalf("completion items = %d, want 3", len(e.completionItems))
	}
	if e.completionItems[0].Text != "internal/editor/" {
		t.Errorf("first item = %q, want %q", e.completionItems[0].Text, "internal/editor/")
	}

	// Verify filter is reset
	if e.completionFilter != "" {
		t.Errorf("filter = %q, want empty", e.completionFilter)
	}

	// Verify index is reset
	if e.completionIndex != 0 {
		t.Errorf("index = %d, want 0", e.completionIndex)
	}
}

// TestDrillUp verifies drilling up restores parent directory state.
func TestDrillUp(t *testing.T) {
	// Set up a CompleteFunc that returns root-level items
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
		config:  Config{CompleteFunc: completeFunc},
	}
	// Simulate being drilled into "internal/" with buffer "ls internal/"
	// completionCol points to word start (3), items have full paths
	e.state.Buffer = NewBufferFromString("ls internal/")
	e.state.Cursor.MoveTo(0, 12)
	e.completionActive = true
	e.completionCol = 3 // Word start (where "internal/" begins)
	e.completionPrefix = "internal/"
	e.completionFilter = ""
	e.completionIndex = 1
	e.completionItems = []Completion{
		{Text: "internal/editor/", Description: "directory"},
		{Text: "internal/parser/", Description: "directory"},
		{Text: "internal/shell/", Description: "directory"},
	}
	// The drill stack stores the parent state, including the col before drilling
	e.completionDrillStack = []completionDrillState{
		{prefix: "int", filter: "in", index: 0, col: 3},
	}

	// Drill up
	e.drillUp()

	// Verify buffer restored to parent: "internal/" segment removed from col 3 onwards
	if got := e.state.Buffer.Content(); got != "ls " {
		t.Errorf("buffer = %q, want %q", got, "ls ")
	}

	// Verify cursor position at restored col
	if e.state.Cursor.Pos.Col != 3 {
		t.Errorf("cursor col = %d, want 3", e.state.Cursor.Pos.Col)
	}

	// Verify drill stack is empty
	if len(e.completionDrillStack) != 0 {
		t.Errorf("drill stack length = %d, want 0", len(e.completionDrillStack))
	}

	// Verify completionCol restored
	if e.completionCol != 3 {
		t.Errorf("completionCol = %d, want 3", e.completionCol)
	}

	// Verify items re-queried for parent
	if len(e.completionItems) != 3 {
		t.Fatalf("completion items = %d, want 3", len(e.completionItems))
	}
	if e.completionItems[0].Text != "internal/" {
		t.Errorf("first item = %q, want %q", e.completionItems[0].Text, "internal/")
	}

	// Verify filter and index restored
	if e.completionFilter != "in" {
		t.Errorf("filter = %q, want %q", e.completionFilter, "in")
	}
	if e.completionIndex != 0 {
		t.Errorf("index = %d, want 0", e.completionIndex)
	}
	if e.completionPrefix != "int" {
		t.Errorf("prefix = %q, want %q", e.completionPrefix, "int")
	}
}

// TestAutodrillSingleChild verifies that when a directory contains only
// one subdirectory, drilling auto-continues recursively.
func TestAutodrillSingleChild(t *testing.T) {
	// CompleteFunc returns single-child directories for two levels, then multiple items
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
		config:  Config{CompleteFunc: completeFunc},
	}
	e.state.Buffer = NewBufferFromString("ls s")
	e.state.Cursor.MoveTo(0, 4)
	e.completionActive = true
	e.completionCol = 3
	e.completionPrefix = "s"
	e.completionFilter = ""
	e.completionIndex = 0
	e.completionItems = []Completion{
		{Text: "src/", Description: "directory"},
		{Text: "README.md", Description: "file"},
	}

	// Drill into "src/" — should auto-drill through "main/" and "go/"
	e.drillIntoDirectory(Completion{Text: "src/", Description: "directory"})

	// Should have auto-drilled through src/ -> main/ -> go/
	// Drill stack should have 3 entries (one for each level: original, src/, main/)
	if len(e.completionDrillStack) != 3 {
		t.Fatalf("drill stack length = %d, want 3 (auto-drilled through single-child dirs)", len(e.completionDrillStack))
	}

	// Buffer should show the fully drilled path
	if got := e.state.Buffer.Content(); got != "ls src/main/go/" {
		t.Errorf("buffer = %q, want %q", got, "ls src/main/go/")
	}

	// Final items should be the files in go/
	if len(e.completionItems) != 2 {
		t.Fatalf("completion items = %d, want 2", len(e.completionItems))
	}
	if e.completionItems[0].Text != "src/main/go/handler.go" {
		t.Errorf("first item = %q, want %q", e.completionItems[0].Text, "src/main/go/handler.go")
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
		config:  Config{CompleteFunc: completeFunc},
	}
	e.state.Buffer = NewBufferFromString("ls emp")
	e.state.Cursor.MoveTo(0, 6)
	e.completionActive = true
	e.completionCol = 3
	e.completionPrefix = "emp"
	e.completionFilter = ""
	e.completionIndex = 0
	e.completionItems = []Completion{
		{Text: "emptdir/", Description: "directory"},
		{Text: "file.txt", Description: "file"},
	}

	e.drillIntoDirectory(Completion{Text: "emptdir/", Description: "directory"})

	// Empty directory should dismiss completion
	if e.completionActive {
		t.Error("expected completion to be dismissed for empty directory")
	}

	// Drill stack should be empty (the push was rolled back)
	if len(e.completionDrillStack) != 0 {
		t.Errorf("drill stack length = %d, want 0", len(e.completionDrillStack))
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
	e.completionDrillStack = nil

	// Should not panic
	e.drillUp()

	// Buffer unchanged
	if got := e.state.Buffer.Content(); got != "ls foo" {
		t.Errorf("buffer = %q, want %q", got, "ls foo")
	}
}

// TestSingleDirectoryShowsMenu verifies that a single directory match
// shows the completion menu instead of auto-inserting.
func TestSingleDirectoryShowsMenu(t *testing.T) {
	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		config: Config{
			CompleteFunc: func(line string, pos int) []Completion {
				return []Completion{
					{Text: "internal/", Description: "directory"},
				}
			},
		},
	}
	e.state.Buffer = NewBufferFromString("ls int")
	e.state.Cursor.MoveTo(0, 6)

	e.triggerCompletion()

	if !e.completionActive {
		t.Error("expected completion menu to be active for single directory match")
	}
	if len(e.completionItems) != 1 {
		t.Fatalf("expected 1 completion item, got %d", len(e.completionItems))
	}
	if e.completionItems[0].Text != "internal/" {
		t.Errorf("completion item = %q, want %q", e.completionItems[0].Text, "internal/")
	}
}

// TestSingleFileAutoInserts verifies that a single file match is
// auto-inserted without showing the completion menu.
func TestSingleFileAutoInserts(t *testing.T) {
	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		config: Config{
			CompleteFunc: func(line string, pos int) []Completion {
				return []Completion{
					{Text: "go.mod", Description: "go file"},
				}
			},
		},
	}
	e.state.Buffer = NewBufferFromString("ls go.m")
	e.state.Cursor.MoveTo(0, 7)

	e.triggerCompletion()

	if e.completionActive {
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
	e.completionIndex = 0

	e.handleCompletionKey(Key{Special: KeyTab})
	if e.completionIndex != 1 {
		t.Errorf("expected index 1 after first Tab, got %d", e.completionIndex)
	}
	e.handleCompletionKey(Key{Special: KeyTab})
	if e.completionIndex != 2 {
		t.Errorf("expected index 2 after second Tab, got %d", e.completionIndex)
	}
	e.handleCompletionKey(Key{Special: KeyTab})
	if e.completionIndex != 0 {
		t.Errorf("expected index 0 after wrap, got %d", e.completionIndex)
	}
	if !e.completionActive {
		t.Error("expected completion to remain active after Tab cycling")
	}
}

// TestHandleCompletionKeyEnterDirectory verifies that Enter on a directory drills into it.
func TestHandleCompletionKeyEnterDirectory(t *testing.T) {
	completeFunc := func(line string, pos int) []Completion {
		return []Completion{
			{Text: "internal/editor/", Description: "directory"},
			{Text: "internal/parser/", Description: "directory"},
		}
	}

	e := newTestEditorForCompletion([]Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "cmd/", Description: "directory"},
		{Text: "go.mod", Description: "file"},
	})
	e.config.CompleteFunc = completeFunc
	e.state.Buffer = NewBufferFromString("ls int")
	e.state.Cursor.MoveTo(0, 6)
	e.completionCol = 3
	e.completionPrefix = "int"
	e.completionIndex = 0 // "internal/"

	handled := e.handleCompletionKey(Key{Special: KeyEnter})
	if !handled {
		t.Fatal("expected Enter to be handled")
	}

	if !e.completionActive {
		t.Error("expected completion to remain active after drilling into directory")
	}
	if len(e.completionDrillStack) == 0 {
		t.Error("expected non-empty drill stack after drilling")
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
	e.completionCol = 3
	e.completionPrefix = "go"
	e.completionIndex = 1 // "go.mod"

	handled := e.handleCompletionKey(Key{Special: KeyEnter})
	if !handled {
		t.Fatal("expected Enter to be handled")
	}

	if e.completionActive {
		t.Error("expected completion to be dismissed after accepting file")
	}
	if got := e.state.Buffer.Content(); got != "ls go.mod" {
		t.Errorf("buffer = %q, want %q", got, "ls go.mod")
	}
}

// TestHandleCompletionKeyTyping verifies that printable characters update the filter.
func TestHandleCompletionKeyTyping(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
		{Text: "init.go", Description: "file"},
		{Text: "README.md", Description: "file"},
	}
	e := newTestEditorForCompletion(items)

	// Type 'i' — filter should be "i", filtered items: internal/, interface.go, init.go
	handled := e.handleCompletionKey(Key{Rune: 'i'})
	if !handled {
		t.Fatal("expected key 'i' to be handled")
	}
	if e.completionFilter != "i" {
		t.Errorf("filter = %q, want %q", e.completionFilter, "i")
	}
	filtered := e.filteredCompletionItems()
	if len(filtered) != 3 {
		t.Fatalf("expected 3 filtered items after 'i', got %d", len(filtered))
	}
	if e.completionIndex != 0 {
		t.Errorf("completionIndex = %d, want 0 after typing", e.completionIndex)
	}

	// Type 'n' — filter should be "in", filtered items: internal/, interface.go, init.go
	handled = e.handleCompletionKey(Key{Rune: 'n'})
	if !handled {
		t.Fatal("expected key 'n' to be handled")
	}
	if e.completionFilter != "in" {
		t.Errorf("filter = %q, want %q", e.completionFilter, "in")
	}
	filtered = e.filteredCompletionItems()
	if len(filtered) != 3 {
		t.Fatalf("expected 3 filtered items after 'in', got %d", len(filtered))
	}

	// Type 't' — filter should be "int", filtered items: internal/, interface.go
	handled = e.handleCompletionKey(Key{Rune: 't'})
	if !handled {
		t.Fatal("expected key 't' to be handled")
	}
	if e.completionFilter != "int" {
		t.Errorf("filter = %q, want %q", e.completionFilter, "int")
	}
	filtered = e.filteredCompletionItems()
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered items after 'int', got %d", len(filtered))
	}

	// Backspace — filter should go back to "in"
	handled = e.handleCompletionKey(Key{Special: KeyBackspace})
	if !handled {
		t.Fatal("expected Backspace to be handled")
	}
	if e.completionFilter != "in" {
		t.Errorf("filter = %q, want %q after backspace", e.completionFilter, "in")
	}
	filtered = e.filteredCompletionItems()
	if len(filtered) != 3 {
		t.Fatalf("expected 3 filtered items after backspace to 'in', got %d", len(filtered))
	}

	// Backspace — filter should go back to "i"
	handled = e.handleCompletionKey(Key{Special: KeyBackspace})
	if !handled {
		t.Fatal("expected Backspace to be handled")
	}
	if e.completionFilter != "i" {
		t.Errorf("filter = %q, want %q after second backspace", e.completionFilter, "i")
	}
}

// TestHandleCompletionKeyNavigation verifies Up/Down cycle through filtered items only.
func TestHandleCompletionKeyNavigation(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
		{Text: "init.go", Description: "file"},
		{Text: "README.md", Description: "file"},
	}
	e := newTestEditorForCompletion(items)

	// Set filter to "int" — only internal/ and interface.go match
	e.completionFilter = "int"
	filtered := e.filteredCompletionItems()
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered items for 'int', got %d", len(filtered))
	}

	// Start at index 0 (internal/)
	if e.completionIndex != 0 {
		t.Fatalf("expected initial index 0, got %d", e.completionIndex)
	}

	// Down → index 1 (interface.go)
	e.handleCompletionKey(Key{Special: KeyDown})
	if e.completionIndex != 1 {
		t.Errorf("after Down: index = %d, want 1", e.completionIndex)
	}

	// Down → wraps to index 0 (internal/)
	e.handleCompletionKey(Key{Special: KeyDown})
	if e.completionIndex != 0 {
		t.Errorf("after second Down: index = %d, want 0 (wrap)", e.completionIndex)
	}

	// Up → wraps to index 1 (last filtered item)
	e.handleCompletionKey(Key{Special: KeyUp})
	if e.completionIndex != 1 {
		t.Errorf("after Up from 0: index = %d, want 1 (wrap)", e.completionIndex)
	}

	// Up → back to index 0
	e.handleCompletionKey(Key{Special: KeyUp})
	if e.completionIndex != 0 {
		t.Errorf("after second Up: index = %d, want 0", e.completionIndex)
	}
}

// TestHandleCompletionKeyAccept verifies Enter and Right accept the filtered item.
func TestHandleCompletionKeyAccept(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
		{Text: "init.go", Description: "file"},
		{Text: "README.md", Description: "file"},
	}

	t.Run("enter_accepts_filtered", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		e.completionFilter = "int"
		e.completionIndex = 1 // interface.go in filtered list

		handled := e.handleCompletionKey(Key{Special: KeyEnter})
		if !handled {
			t.Fatal("expected Enter to be handled")
		}
		// After accepting, completion should be dismissed
		if e.completionActive {
			t.Error("expected completion to be dismissed after Enter")
		}
	})

	t.Run("right_accepts_filtered", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		e.completionFilter = "int"
		e.completionIndex = 0 // internal/ in filtered list

		handled := e.handleCompletionKey(Key{Special: KeyRight})
		if !handled {
			t.Fatal("expected Right to be handled")
		}
		if e.completionActive {
			t.Error("expected completion to be dismissed after Right")
		}
	})

	t.Run("enter_with_empty_filtered_dismisses", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		e.completionFilter = "xyz" // no matches

		handled := e.handleCompletionKey(Key{Special: KeyEnter})
		if !handled {
			t.Fatal("expected Enter to be handled")
		}
		if e.completionActive {
			t.Error("expected completion to be dismissed when no filtered items")
		}
	})
}

// TestHandleCompletionKeyDismiss verifies Escape and Backspace dismiss behavior.
func TestHandleCompletionKeyDismiss(t *testing.T) {
	items := []Completion{
		{Text: "internal/", Description: "directory"},
		{Text: "interface.go", Description: "file"},
	}

	t.Run("escape_dismisses", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		e.completionFilter = "int"

		handled := e.handleCompletionKey(Key{Special: KeyEscape})
		if !handled {
			t.Fatal("expected Escape to be handled")
		}
		if e.completionActive {
			t.Error("expected completion to be dismissed after Escape")
		}
		if e.completionFilter != "" {
			t.Errorf("expected filter cleared, got %q", e.completionFilter)
		}
	})

	t.Run("backspace_empty_filter_no_drill_dismisses", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		e.completionFilter = ""
		e.completionDrillStack = nil

		handled := e.handleCompletionKey(Key{Special: KeyBackspace})
		if handled {
			t.Fatal("expected Backspace with empty filter and no drill stack to return false")
		}
		if e.completionActive {
			t.Error("expected completion to be dismissed")
		}
	})

	t.Run("backspace_empty_filter_with_drill_drills_up", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		e.state = NewEditorState()
		e.state.Buffer = NewBufferFromString("ls src/main/")
		e.state.Cursor.MoveTo(0, 12)
		e.completionCol = 12 // After drill, col is at cursor
		e.completionFilter = ""
		e.completionPrefix = "main/"
		e.config.CompleteFunc = func(line string, pos int) []Completion {
			return items
		}
		e.completionDrillStack = []completionDrillState{
			{prefix: "src/", filter: "", index: 0, col: 3},
		}

		handled := e.handleCompletionKey(Key{Special: KeyBackspace})
		if !handled {
			t.Fatal("expected Backspace with drill stack to return true (drillUp stays in menu)")
		}
		if !e.completionActive {
			t.Error("expected completion to remain active after drillUp")
		}
		if len(e.completionDrillStack) != 0 {
			t.Errorf("expected empty drill stack after drillUp, got %d", len(e.completionDrillStack))
		}
	})

	t.Run("non_printable_dismisses", func(t *testing.T) {
		e := newTestEditorForCompletion(items)
		// Ctrl+A is non-printable
		handled := e.handleCompletionKey(Key{Rune: 'a', Ctrl: true})
		if handled {
			t.Fatal("expected Ctrl+A to pass through (not handled)")
		}
		if e.completionActive {
			t.Error("expected completion to be dismissed on non-printable key")
		}
	})
}
