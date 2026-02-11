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

func TestIntegration_DirectoryListingAfterCompletion(t *testing.T) {
	// Simulates: user types "cd my", gets "mydir/", then presses TAB again
	// The second TAB should list contents of mydir/
	tmpDir := t.TempDir()
	myDir := filepath.Join(tmpDir, "mydir")
	os.Mkdir(myDir, 0755)
	os.WriteFile(filepath.Join(myDir, "file1.txt"), []byte(""), 0644)
	os.WriteFile(filepath.Join(myDir, "file2.txt"), []byte(""), 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	router := NewRouter()
	router.SetFuzzy(true)

	fileCompleter := NewFileCompleter()
	fileCompleter.SetFuzzyMode(true)
	router.Register(fileCompleter, PriorityFilesystem)

	// First completion: "cd my" -> should return "mydir/"
	result1, _ := router.Complete(context.Background(), "cd my", 5)
	if len(result1.Items) != 1 || result1.Items[0].Value != "mydir/" {
		t.Fatalf("First completion should return mydir/, got: %v", result1.Items)
	}

	// Second completion: "cd mydir/" -> should list dir contents (unfiltered)
	result2, _ := router.Complete(context.Background(), "cd mydir/", 9)
	if len(result2.Items) != 2 {
		t.Errorf("Second TAB should list 2 files in mydir/, got %d", len(result2.Items))
	}
}

func TestIntegration_NestedDirectoryCompletion(t *testing.T) {
	// Test completing deeply nested paths
	tmpDir := t.TempDir()
	nested := filepath.Join(tmpDir, "a", "b", "c")
	os.MkdirAll(nested, 0755)
	os.WriteFile(filepath.Join(nested, "deep.txt"), []byte(""), 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	router := NewRouter()
	router.SetFuzzy(true)

	fileCompleter := NewFileCompleter()
	fileCompleter.SetFuzzyMode(true)
	router.Register(fileCompleter, PriorityFilesystem)

	// Complete "cat a/b/c/" - should find deep.txt
	result, _ := router.Complete(context.Background(), "cat a/b/c/", 10)
	if len(result.Items) != 1 || result.Items[0].Value != "deep.txt" {
		t.Errorf("Should find deep.txt in nested path, got: %v", result.Items)
	}
}

func TestIntegration_MixedCaseCompletion(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "Readme.txt"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme"), []byte(""), 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	router := NewRouter()
	router.SetFuzzy(true)

	fileCompleter := NewFileCompleter()
	fileCompleter.SetFuzzyMode(true)
	router.Register(fileCompleter, PriorityFilesystem)

	// "READ" should match all three via case-insensitive fuzzy
	result, _ := router.Complete(context.Background(), "cat READ", 8)
	if len(result.Items) != 3 {
		t.Errorf("Case-insensitive fuzzy should match all 3, got %d", len(result.Items))
	}

	// But "readme" (exact case) should sort highest
	if result.Items[0].Value != "readme" {
		t.Errorf("Exact case match should be first, got %q", result.Items[0].Value)
	}
}

func TestIntegration_GitBranchLikeCompletion(t *testing.T) {
	// Test fuzzy matching for branch-like names (common real-world use)
	items := []Item{
		{Value: "feature/add-login", Display: "feature/add-login"},
		{Value: "feature/add-logout", Display: "feature/add-logout"},
		{Value: "fix/login-bug", Display: "fix/login-bug"},
		{Value: "main", Display: "main"},
	}

	// "flog" should match both feature/add-login and fix/login-bug
	result := FuzzyFilter(items, "flog")
	if len(result) < 2 {
		t.Errorf("'flog' should match multiple branches, got %d", len(result))
	}

	// "feat" should match both feature branches
	result2 := FuzzyFilter(items, "feat")
	if len(result2) != 2 {
		t.Errorf("'feat' should match 2 feature branches, got %d", len(result2))
	}
}

func TestIntegration_CDCompletionUsesFilesystem(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, "site"), 0o755); err != nil {
		t.Fatalf("mkdir site: %v", err)
	}

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	router := NewRouter()
	router.Register(NewAliasCompleter(&mockFunctionProvider{functions: []string{"site"}}), PriorityAlias)
	router.Register(NewFileCompleter(), PriorityFilesystem)

	result, err := router.Complete(context.Background(), "cd si", len("cd si"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 filesystem completion for cd argument, got %d: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "site/" {
		t.Fatalf("expected directory completion site/, got %q", result.Items[0].Value)
	}
}
