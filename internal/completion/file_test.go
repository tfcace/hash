package completion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileCompleter_CurrentDir(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "foo.txt"), []byte{}, 0644)
	os.WriteFile(filepath.Join(tmpDir, "bar.txt"), []byte{}, 0644)
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0o755) //nolint:gosec // G301: test directory

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

func TestFileCompleter_SymlinkToDirectory(t *testing.T) {
	// Create temp directory with a subdirectory and a symlink to it
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "actual_dir")
	os.Mkdir(subDir, 0o755) //nolint:gosec // G301: test directory
	symlink := filepath.Join(tmpDir, "symlink_dir")
	if err := os.Symlink(subDir, symlink); err != nil {
		t.Skipf("Cannot create symlink: %v", err)
	}

	completer := NewFileCompleter()

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	result, err := completer.Complete(context.Background(), "cd ", 3)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Both actual_dir and symlink_dir should have trailing slashes
	// because symlink_dir points to a directory
	for _, item := range result.Items {
		if item.Value == "symlink_dir/" {
			return // Found it with trailing slash - success
		}
		if item.Value == "symlink_dir" {
			t.Errorf("Symlink to directory should have trailing slash, got %q", item.Value)
			return
		}
	}
	t.Error("symlink_dir not found in completions")
}

func TestFileCompleter_HiddenFiles(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, ".hidden"), []byte{}, 0644)
	os.WriteFile(filepath.Join(tmpDir, "visible.txt"), []byte{}, 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Default: hidden files should NOT be shown
	result, _ := completer.Complete(ctx, "cat ", 4)
	for _, item := range result.Items {
		if item.Value == ".hidden" {
			t.Error("Hidden files should not be shown by default")
		}
	}

	// Enable hidden files
	completer.SetShowHidden(true)
	result2, _ := completer.Complete(ctx, "cat ", 4)
	found := false
	for _, item := range result2.Items {
		if item.Value == ".hidden" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Hidden files should be shown when enabled")
	}

	// Test: hidden files should be shown when prefix starts with "."
	completer.SetShowHidden(false) // Reset to default
	result3, _ := completer.Complete(ctx, "cat .", 5)
	foundWithDotPrefix := false
	for _, item := range result3.Items {
		if item.Value == ".hidden" {
			foundWithDotPrefix = true
			break
		}
	}
	if !foundWithDotPrefix {
		t.Error("Hidden files should be shown when prefix starts with '.'")
	}
}

func TestFileCompleter_AbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "target.txt"), []byte{}, 0644)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete with absolute path
	result, err := completer.Complete(ctx, "cat "+tmpDir+"/tar", len("cat "+tmpDir+"/tar"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("Expected 1 completion for absolute path, got %d", len(result.Items))
	}
	if len(result.Items) > 0 && result.Items[0].Value != "target.txt" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "target.txt")
	}
}

func TestFileCompleter_DirectoryWithTrailingSlash(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "mydir")
	os.Mkdir(subDir, 0o755) //nolint:gosec // G301: test directory
	os.WriteFile(filepath.Join(subDir, "inner.txt"), []byte{}, 0o644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete "cd mydir/" should list contents of mydir
	result, err := completer.Complete(ctx, "cd mydir/", 9)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("Expected 1 file in mydir/, got %d", len(result.Items))
	}
	if len(result.Items) > 0 && result.Items[0].Value != "inner.txt" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "inner.txt")
	}
}

func TestFileCompleter_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	emptyDir := filepath.Join(tmpDir, "empty")
	os.Mkdir(emptyDir, 0o755) //nolint:gosec // G301: test directory

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete "cd empty/" should return no items (empty dir)
	result, err := completer.Complete(ctx, "cd empty/", 9)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("Empty directory should have no completions, got %d", len(result.Items))
	}
}

func TestFileCompleter_NonexistentDirectory(t *testing.T) {
	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete path to nonexistent directory should return empty, not error
	result, err := completer.Complete(ctx, "cd /nonexistent/path/", 21)
	if err != nil {
		t.Fatalf("Should not error on nonexistent path, got %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("Nonexistent path should have no completions, got %d", len(result.Items))
	}
}

func TestFileCompleter_CaseInsensitivePrefix(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte{}, 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte{}, 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// "read" should match both "README.md" and "readme.txt" (case insensitive)
	result, _ := completer.Complete(ctx, "cat read", 8)
	if len(result.Items) != 2 {
		t.Errorf("Case-insensitive prefix should match 2 files, got %d", len(result.Items))
	}
}

func TestFileCompleter_RootFilesystem(t *testing.T) {
	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete "cd /t" should return prefix "/" not "//"
	result, err := completer.Complete(ctx, "cd /t", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// The prefix should be "/" not "//"
	if result.Prefix != "/" {
		t.Errorf("Prefix = %q, want %q", result.Prefix, "/")
	}

	// Verify items don't have double slashes when combined with prefix
	for _, item := range result.Items {
		fullPath := result.Prefix + item.Value
		if strings.Contains(fullPath, "//") {
			t.Errorf("Full path contains double slash: %q", fullPath)
		}
	}
}

func TestFileCompleter_DotSlashPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	os.Mkdir(filepath.Join(tmpDir, "scripts"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "scripts", "build.sh"), []byte{}, 0755)
	os.WriteFile(filepath.Join(tmpDir, "scripts", "test.sh"), []byte{}, 0755)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := NewFileCompleter()
	ctx := context.Background()

	// Complete "./scripts/buil" should preserve "./" prefix
	result, err := completer.Complete(ctx, "./scripts/buil", 14)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Prefix should be "./scripts/" not "scripts/"
	if result.Prefix != "./scripts/" {
		t.Errorf("Prefix = %q, want %q", result.Prefix, "./scripts/")
	}

	// Verify the full path includes "./"
	for _, item := range result.Items {
		fullPath := result.Prefix + item.Value
		if !strings.HasPrefix(fullPath, "./") {
			t.Errorf("Full path should start with './', got %q", fullPath)
		}
	}
}
