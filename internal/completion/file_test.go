package completion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileCompleter_CurrentDir(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "foo.txt"), []byte{}, 0644)
	os.WriteFile(filepath.Join(tmpDir, "bar.txt"), []byte{}, 0644)
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)

	// Change to temp dir
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	result, err := completer.Complete(ctx, "cat ", 4)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) < 2 {
		t.Errorf("Items count = %d, want >= 2", len(result.Items))
	}

	// Should contain foo.txt and bar.txt
	values := make(map[string]bool)
	for _, item := range result.Items {
		values[item.Value] = true
	}
	if !values["foo.txt"] {
		t.Error("Missing foo.txt in completions")
	}
	if !values["bar.txt"] {
		t.Error("Missing bar.txt in completions")
	}
}

func TestFileCompleter_PartialMatch(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte{}, 0644)
	os.WriteFile(filepath.Join(tmpDir, "makefile"), []byte{}, 0644)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	result, err := completer.Complete(ctx, "cat RE", 6)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("Items count = %d, want 1", len(result.Items))
	}
	if result.Items[0].Value != "README.md" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "README.md")
	}
}

func TestFileCompleter_TildeExpansion(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}

	completer := NewFileCompleter()
	ctx := context.Background()

	result, err := completer.Complete(ctx, "cd ~/", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should have completions from home directory
	if len(result.Items) == 0 {
		t.Error("No completions for ~/")
	}
}

func TestFileCompleter_Name(t *testing.T) {
	completer := NewFileCompleter()
	if completer.Name() != "file" {
		t.Errorf("Name() = %q, want %q", completer.Name(), "file")
	}
}

func TestFileCompleter_FuzzyMode(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "context.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "container.yaml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte(""), 0644)

	completer := NewFileCompleter()
	completer.SetFuzzyMode(true)

	// Change to temp dir for completion
	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	// With fuzzy mode, "cont" should return ALL files (router will filter)
	result, err := completer.Complete(context.Background(), "cat cont", 8)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should return all 4 files, not just prefix matches
	if len(result.Items) != 4 {
		t.Errorf("FuzzyMode should return all candidates, got %d items: %v", len(result.Items), result.Items)
	}
}

func TestFileCompleter_PrefixMode(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "context.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte(""), 0644)

	completer := NewFileCompleter()
	completer.SetFuzzyMode(false) // Default - prefix only

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	// Without fuzzy mode, "con" should only return prefix matches
	result, err := completer.Complete(context.Background(), "cat con", 7)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should only return config.toml and context.go (prefix matches)
	if len(result.Items) != 2 {
		t.Errorf("PrefixMode should return only prefix matches, got %d items", len(result.Items))
	}
}
