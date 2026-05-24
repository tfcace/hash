package editor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/completion"
)

// createIntegrationTestDir creates a temp directory with:
//
//	testdir/
//	  src/
//	    main/
//	      app.go
//	      util.go
//	  docs/
//	    readme.md
//	  go.mod
//	  config.toml
func createIntegrationTestDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()

	dirs := []string{
		filepath.Join(tmpDir, "src", "main"),
		filepath.Join(tmpDir, "docs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", d, err)
		}
	}

	files := map[string]string{
		filepath.Join(tmpDir, "src", "main", "app.go"):  "package main\n",
		filepath.Join(tmpDir, "src", "main", "util.go"): "package main\n",
		filepath.Join(tmpDir, "docs", "readme.md"):      "# Readme\n",
		filepath.Join(tmpDir, "go.mod"):                 "module test\n",
		filepath.Join(tmpDir, "config.toml"):            "[config]\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	return tmpDir
}

// makeRealCompleteFunc builds a completeFunc backed by the real completion
// router and file completer. This mirrors makeEditorCompleteFunc in
// internal/shell/shell.go but avoids a circular import.
func makeRealCompleteFunc(t *testing.T) func(string, int) []Completion {
	t.Helper()
	router := completion.NewRouter()
	fc := completion.NewFileCompleter()
	router.Register(fc, completion.PriorityFilesystem)

	return func(line string, pos int) []Completion {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		result, err := router.Complete(ctx, line, pos)
		if err != nil || len(result.Items) == 0 {
			return nil
		}

		items := make([]Completion, len(result.Items))
		for i, item := range result.Items {
			items[i] = Completion{
				Text:        result.Prefix + item.Value,
				Description: item.Description,
			}
		}
		return items
	}
}

// newTestEditorWithRealCompletion creates an Editor wired to the real file
// completer, with buffer set to the given content and cursor at the given col.
func newTestEditorWithRealCompletion(t *testing.T, bufContent string, cursorCol int) *Editor {
	t.Helper()
	completeFunc := makeRealCompleteFunc(t)
	e := &Editor{
		display: newTestDisplay(),
		state:   NewEditorState(),
		config:  Config{CompleteFunc: completeFunc},
		ghost:   NewGhostText(),
	}
	e.state.Buffer = NewBufferFromString(bufContent)
	e.state.Cursor.MoveTo(0, cursorCol)
	return e
}

func completionTextList(items []Completion) []string {
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.Text
	}
	return texts
}

// TestIntegration_DrillDown verifies that drilling into a directory
// with the real file completer updates the buffer and returns child items
// with full relative paths.
func TestIntegration_DrillDown(t *testing.T) {
	tmpDir := createIntegrationTestDir(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	e := newTestEditorWithRealCompletion(t, "ls ", 3)

	// Trigger completion to get root items
	e.triggerCompletion()
	if !e.completionActive {
		t.Fatal("expected completion to be active after trigger")
	}

	// Find src/ item
	srcItem, srcIdx := findCompletionItem(t, e.completionItems, "src/")
	_ = srcIdx

	// Verify completionCol is at word start (3, after "ls ")
	if e.completionCol != 3 {
		t.Errorf("completionCol = %d, want 3", e.completionCol)
	}

	// Drill into src/. Since src/ has only one child (main/), auto-drill
	// fires immediately, so the buffer jumps straight to "ls src/main/".
	e.drillIntoDirectory(srcItem)

	if got := e.state.Buffer.Content(); got != "ls src/main/" {
		t.Errorf("buffer after drill+auto-drill = %q, want %q", got, "ls src/main/")
	}

	// Items should include src/main/app.go and src/main/util.go
	itemTexts := completionTextList(e.completionItems)
	assertContains(t, itemTexts, "src/main/app.go")
	assertContains(t, itemTexts, "src/main/util.go")

	// completionCol should still be 3 (word start, not end of "src/main/")
	if e.completionCol != 3 {
		t.Errorf("completionCol after drill = %d, want 3", e.completionCol)
	}
}

// TestIntegration_DrillDownThenAcceptFile verifies that after drilling into
// src/main/, accepting a file produces the correct buffer content.
func TestIntegration_DrillDownThenAcceptFile(t *testing.T) {
	tmpDir := createIntegrationTestDir(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	e := newTestEditorWithRealCompletion(t, "ls ", 3)

	// Trigger and drill into src/ (auto-drills through main/)
	e.triggerCompletion()
	if !e.completionActive {
		t.Fatal("expected completion active")
	}

	srcItem, _ := findCompletionItem(t, e.completionItems, "src/")
	e.drillIntoDirectory(srcItem)

	// Should be at src/main/ after auto-drill
	if got := e.state.Buffer.Content(); got != "ls src/main/" {
		t.Fatalf("buffer = %q, want %q", got, "ls src/main/")
	}

	// Find app.go and accept it
	appItem, _ := findCompletionItem(t, e.completionItems, "src/main/app.go")
	e.acceptCompletion(appItem)

	// Verify buffer
	if got := e.state.Buffer.Content(); got != "ls src/main/app.go" {
		t.Errorf("buffer after accept = %q, want %q", got, "ls src/main/app.go")
	}

	// Completion should be dismissed
	if e.completionActive {
		t.Error("expected completion dismissed after accepting file")
	}
}

func TestIntegration_AcceptFileWithSpacesEscapesPath(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "My File.txt"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	e := newTestEditorWithRealCompletion(t, "ls My", 5)
	e.triggerCompletion()

	if got := e.state.Buffer.Content(); got != `ls My\ File.txt` {
		t.Fatalf("buffer after single completion = %q, want %q", got, `ls My\ File.txt`)
	}
	if e.completionActive {
		t.Fatal("single file completion should be accepted immediately")
	}
}

func TestIntegration_DrillIntoDirectoryWithSpaces(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "My Dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Child File.txt"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	e := newTestEditorWithRealCompletion(t, "ls My", 5)
	e.triggerCompletion()
	if !e.completionActive {
		t.Fatal("expected directory completion menu to be active")
	}

	dirItem, _ := findCompletionItem(t, e.completionItems, `My\ Dir/`)
	e.drillIntoDirectory(dirItem)

	if got := e.state.Buffer.Content(); got != `ls My\ Dir/` {
		t.Fatalf("buffer after drill = %q, want %q", got, `ls My\ Dir/`)
	}

	childItem, _ := findCompletionItem(t, e.completionItems, `My\ Dir/Child\ File.txt`)
	e.acceptCompletion(childItem)

	if got := e.state.Buffer.Content(); got != `ls My\ Dir/Child\ File.txt` {
		t.Fatalf("buffer after accepting child = %q, want %q", got, `ls My\ Dir/Child\ File.txt`)
	}
}

// TestIntegration_DrillUp verifies that drilling up from src/main/ restores
// the parent directory state and re-queries items.
func TestIntegration_DrillUp(t *testing.T) {
	tmpDir := createIntegrationTestDir(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	e := newTestEditorWithRealCompletion(t, "ls ", 3)

	// Trigger and drill into src/ -> auto-drill to src/main/
	e.triggerCompletion()
	if !e.completionActive {
		t.Fatal("expected completion active")
	}

	srcItem, _ := findCompletionItem(t, e.completionItems, "src/")
	e.drillIntoDirectory(srcItem)

	// After auto-drill we should be in src/main/ with a drill stack
	if len(e.completionDrillStack) == 0 {
		t.Fatal("expected non-empty drill stack after drill-down")
	}
	drillDepth := len(e.completionDrillStack)

	// Drill up once
	e.drillUp()

	// Drill stack should have decreased
	if len(e.completionDrillStack) != drillDepth-1 {
		t.Errorf("drill stack = %d, want %d", len(e.completionDrillStack), drillDepth-1)
	}

	// Keep drilling up until we reach the root
	for len(e.completionDrillStack) > 0 {
		e.drillUp()
	}

	// Buffer should be back to "ls "
	if got := e.state.Buffer.Content(); got != "ls " {
		t.Errorf("buffer after full drill-up = %q, want %q", got, "ls ")
	}

	// Items should be root-level entries again
	if len(e.completionItems) == 0 {
		t.Fatal("expected items after drill-up, got none")
	}
	itemTexts := completionTextList(e.completionItems)
	assertContains(t, itemTexts, "src/")
	assertContains(t, itemTexts, "docs/")
	assertContains(t, itemTexts, "go.mod")
	assertContains(t, itemTexts, "config.toml")
}

// TestIntegration_HandleCompletionKeyRoundTrip tests the full interaction
// flow using handleCompletionKey: trigger, cycle via Tab, accept directory
// via Enter, re-trigger, and accept file via Enter.
func TestIntegration_HandleCompletionKeyRoundTrip(t *testing.T) {
	tmpDir := createIntegrationTestDir(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	e := newTestEditorWithRealCompletion(t, "ls ", 3)

	// Step 1: Trigger completion
	e.triggerCompletion()
	if !e.completionActive {
		t.Fatal("expected completion active after trigger")
	}
	initialItems := len(e.filteredCompletionItems())
	if initialItems == 0 {
		t.Fatal("expected initial items")
	}

	// Step 2: Navigate to a file with Tab (cycle until we find go.mod)
	goModIdx := -1
	for i, item := range e.filteredCompletionItems() {
		if item.Text == "go.mod" {
			goModIdx = i
			break
		}
	}
	if goModIdx == -1 {
		t.Fatalf("go.mod not found in items: %v", completionTextList(e.filteredCompletionItems()))
	}

	// Cycle to go.mod using Tab keys
	for e.completionIndex != goModIdx {
		e.handleCompletionKey(Key{Special: KeyTab})
	}

	// Step 3: Press Enter to accept go.mod
	handled := e.handleCompletionKey(Key{Special: KeyEnter})
	if !handled {
		t.Fatal("expected Enter to be handled")
	}
	if e.completionActive {
		t.Error("expected completion dismissed after accepting file")
	}
	if got := e.state.Buffer.Content(); got != "ls go.mod" {
		t.Errorf("buffer after accept = %q, want %q", got, "ls go.mod")
	}

	// Step 4: Start new completion for a directory — Enter accepts it as-is
	e.state.Buffer = NewBufferFromString("cd ")
	e.state.Cursor.MoveTo(0, 3)
	e.triggerCompletion()
	if !e.completionActive {
		t.Fatal("expected completion active for directory test")
	}

	// Find src/
	srcIdx := -1
	for i, item := range e.filteredCompletionItems() {
		if item.Text == "src/" {
			srcIdx = i
			break
		}
	}
	if srcIdx == -1 {
		t.Fatalf("src/ not found in items: %v", completionTextList(e.filteredCompletionItems()))
	}

	e.completionIndex = srcIdx
	handled = e.handleCompletionKey(Key{Special: KeyEnter})
	if !handled {
		t.Fatal("expected Enter to be handled for directory accept")
	}
	if e.completionActive {
		t.Error("expected completion dismissed after Enter on directory")
	}
	if got := e.state.Buffer.Content(); got != "cd src/" {
		t.Errorf("buffer after directory accept = %q, want %q", got, "cd src/")
	}
}

// TestIntegration_TabCycling verifies Tab cycles through real filesystem items.
func TestIntegration_TabCycling(t *testing.T) {
	tmpDir := createIntegrationTestDir(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	e := newTestEditorWithRealCompletion(t, "ls ", 3)
	e.triggerCompletion()
	if !e.completionActive {
		t.Fatal("expected completion active")
	}

	nItems := len(e.filteredCompletionItems())
	if nItems < 2 {
		t.Fatalf("expected at least 2 items for cycling test, got %d", nItems)
	}

	// Tab should cycle through all items and wrap
	for i := 0; i < nItems+1; i++ {
		e.handleCompletionKey(Key{Special: KeyTab})
	}

	// After nItems+1 tabs, we should be at index 1 (wrapped past 0)
	// Tab starts at index 0, each tab moves to (index+1) % nItems
	expectedIdx := (nItems + 1) % nItems
	if e.completionIndex != expectedIdx {
		t.Errorf("after %d tabs on %d items: index = %d, want %d", nItems+1, nItems, e.completionIndex, expectedIdx)
	}

	if !e.completionActive {
		t.Error("expected completion still active after cycling")
	}
}

// TestIntegration_NoDotPrefix verifies that items from the real file completer
// never have a stray "." prefix (the bug that triggered writing these tests).
func TestIntegration_NoDotPrefix(t *testing.T) {
	tmpDir := createIntegrationTestDir(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	e := newTestEditorWithRealCompletion(t, "ls ", 3)
	e.triggerCompletion()

	for _, item := range e.completionItems {
		if strings.HasPrefix(item.Text, ".") {
			t.Errorf("item %q starts with '.'; prefix bug has regressed", item.Text)
		}
	}
}

// --- helpers ---

func findCompletionItem(t *testing.T, items []Completion, text string) (Completion, int) {
	t.Helper()
	for i, item := range items {
		if item.Text == text {
			return item, i
		}
	}
	t.Fatalf("item %q not found in %v", text, completionTextList(items))
	return Completion{}, -1
}

func assertContains(t *testing.T, items []string, want string) {
	t.Helper()
	for _, item := range items {
		if item == want {
			return
		}
	}
	t.Errorf("expected %q in %v", want, items)
}
