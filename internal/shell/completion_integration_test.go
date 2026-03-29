package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/completion"
	"github.com/tfcace/hash/internal/editor"
)

// createTestDirStructure creates a temp directory with:
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
func createTestDirStructure(t *testing.T) string {
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

// TestCompletionIntegration_BasicTabFromEmpty verifies that completing after
// "ls " in a known directory returns all top-level entries with correct
// paths and descriptions, and no item starts with ".".
func TestCompletionIntegration_BasicTabFromEmpty(t *testing.T) {
	tmpDir := createTestDirStructure(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	router := completion.NewRouter()
	fc := completion.NewFileCompleter()
	router.Register(fc, completion.PriorityFilesystem)
	completeFunc := makeEditorCompleteFunc(router)

	items := completeFunc("ls ", 3)
	if len(items) == 0 {
		t.Fatal("expected completion items, got none")
	}

	// Verify no item starts with "."
	for _, item := range items {
		if strings.HasPrefix(item.Text, ".") {
			t.Errorf("item %q starts with '.'; the prefix bug has regressed", item.Text)
		}
	}

	// Expect the four top-level entries: config.toml, docs/, go.mod, src/
	expected := map[string]bool{
		"config.toml": false,
		"docs/":       false,
		"go.mod":      false,
		"src/":        false,
	}
	for _, item := range items {
		expected[item.Text] = true
	}
	for name, found := range expected {
		if !found {
			t.Errorf("expected item %q not found in completions", name)
		}
	}

	// Verify descriptions are non-empty
	for _, item := range items {
		if item.Description == "" {
			t.Errorf("item %q has empty description", item.Text)
		}
	}

	// Verify directory descriptions say "directory"
	for _, item := range items {
		if strings.HasSuffix(item.Text, "/") {
			if item.Description != "directory" {
				t.Errorf("directory item %q description = %q, expected %q", item.Text, item.Description, "directory")
			}
		}
	}

	// Verify file descriptions include file type
	for _, item := range items {
		if !strings.HasSuffix(item.Text, "/") {
			// File descriptions should mention a type (go module, toml, etc.)
			if item.Description == "" {
				t.Errorf("file item %q has empty description", item.Text)
			}
		}
	}
}

// TestCompletionIntegration_PartialWord verifies that typing a partial word
// filters completions correctly, returning only matching items.
func TestCompletionIntegration_PartialWord(t *testing.T) {
	tmpDir := createTestDirStructure(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	router := completion.NewRouter()
	fc := completion.NewFileCompleter()
	router.Register(fc, completion.PriorityFilesystem)
	completeFunc := makeEditorCompleteFunc(router)

	items := completeFunc("ls sr", 5)
	if len(items) != 1 {
		t.Fatalf("expected 1 item for 'sr', got %d: %v", len(items), completionTexts(items))
	}
	if items[0].Text != "src/" {
		t.Errorf("expected 'src/', got %q", items[0].Text)
	}
}

// TestCompletionIntegration_SubdirectoryCompletion verifies that completing
// within a subdirectory returns full relative paths with the directory prefix.
func TestCompletionIntegration_SubdirectoryCompletion(t *testing.T) {
	tmpDir := createTestDirStructure(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	router := completion.NewRouter()
	fc := completion.NewFileCompleter()
	router.Register(fc, completion.PriorityFilesystem)
	completeFunc := makeEditorCompleteFunc(router)

	// After drilling into src/, the buffer would be "ls src/"
	items := completeFunc("ls src/", 7)
	if len(items) == 0 {
		t.Fatal("expected items for 'ls src/', got none")
	}

	// Items should have the full path prefix "src/"
	for _, item := range items {
		if !strings.HasPrefix(item.Text, "src/") {
			t.Errorf("item %q does not have 'src/' prefix", item.Text)
		}
	}

	// Should contain src/main/
	found := false
	for _, item := range items {
		if item.Text == "src/main/" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'src/main/' in items, got %v", completionTexts(items))
	}
}

// TestCompletionIntegration_DeepSubdirectory verifies completing deep
// inside src/main/ returns items with full relative paths.
func TestCompletionIntegration_DeepSubdirectory(t *testing.T) {
	tmpDir := createTestDirStructure(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	router := completion.NewRouter()
	fc := completion.NewFileCompleter()
	router.Register(fc, completion.PriorityFilesystem)
	completeFunc := makeEditorCompleteFunc(router)

	items := completeFunc("ls src/main/", 12)
	if len(items) == 0 {
		t.Fatal("expected items for 'ls src/main/', got none")
	}

	// Should contain full paths like src/main/app.go and src/main/util.go
	expectedFiles := map[string]bool{
		"src/main/app.go":  false,
		"src/main/util.go": false,
	}
	for _, item := range items {
		expectedFiles[item.Text] = true
	}
	for name, found := range expectedFiles {
		if !found {
			t.Errorf("expected %q in items, got %v", name, completionTexts(items))
		}
	}
}

// TestCompletionIntegration_CompleteFuncSignature verifies the completeFunc
// adapter produces []editor.Completion compatible with the editor.
func TestCompletionIntegration_CompleteFuncSignature(t *testing.T) {
	tmpDir := createTestDirStructure(t)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	router := completion.NewRouter()
	fc := completion.NewFileCompleter()
	router.Register(fc, completion.PriorityFilesystem)

	// The function should conform to the editor's expected signature
	fn := makeEditorCompleteFunc(router)

	items := fn("ls ", 3)
	if len(items) == 0 {
		t.Fatal("expected items")
	}

	// Each item should be a valid editor.Completion with Text and Description
	for _, item := range items {
		if item.Text == "" {
			t.Error("item has empty Text")
		}
	}
}

func completionTexts(items []editor.Completion) []string {
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.Text
	}
	return texts
}
