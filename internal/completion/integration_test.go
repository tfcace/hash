package completion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegration_FuzzyCompletion(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()
	files := []string{
		"configuration.yaml",
		"container.go",
		"context_test.go",
		"readme.md",
		"dockerfile",
	}
	for _, f := range files {
		os.WriteFile(filepath.Join(tmpDir, f), []byte(""), 0644)
	}

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	// Test with fuzzy enabled
	router := NewRouter()
	router.SetFuzzy(true)

	fileCompleter := NewFileCompleter()
	fileCompleter.SetFuzzyMode(true)
	router.Register(fileCompleter, PriorityFilesystem)

	// "cfg" should fuzzy match "configuration.yaml"
	result, err := router.Complete(context.Background(), "cat cfg", 7)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should find configuration.yaml via subsequence match
	found := false
	for _, item := range result.Items {
		if item.Value == "configuration.yaml" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Fuzzy should match 'cfg' to 'configuration.yaml', got: %v", result.Items)
	}

	// "ctxgo" should match "context_test.go"
	result2, _ := router.Complete(context.Background(), "cat ctxgo", 9)
	found2 := false
	for _, item := range result2.Items {
		if item.Value == "context_test.go" {
			found2 = true
			break
		}
	}
	if !found2 {
		t.Errorf("Fuzzy should match 'ctxgo' to 'context_test.go', got: %v", result2.Items)
	}
}

func TestIntegration_FuzzyDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "configuration.yaml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "container.go"), []byte(""), 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	router := NewRouter()
	router.SetFuzzy(false)

	fileCompleter := NewFileCompleter()
	fileCompleter.SetFuzzyMode(false)
	router.Register(fileCompleter, PriorityFilesystem)

	// "cfg" should NOT match anything with prefix-only
	result, _ := router.Complete(context.Background(), "cat cfg", 7)
	if len(result.Items) > 0 {
		t.Errorf("Prefix-only mode should not match 'cfg', got: %v", result.Items)
	}

	// "con" should match both configuration and container
	result2, _ := router.Complete(context.Background(), "cat con", 7)
	if len(result2.Items) != 2 {
		t.Errorf("Prefix mode should match 'con*', got %d items", len(result2.Items))
	}
}
