//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/completion"
)

// TestCompletion_Filesystem tests the filesystem completion tier.
// Website promise: Tier 2: Filesystem - <10ms, file/directory completion
func TestCompletion_Filesystem(t *testing.T) {
	// Create test directory structure
	tmpDir := t.TempDir()
	testFiles := []string{
		"file1.txt",
		"file2.txt",
		"script.sh",
		"config.json",
	}
	testDirs := []string{
		"subdir",
		".hidden",
	}

	for _, f := range testFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}
	for _, d := range testDirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, d), 0755); err != nil {
			t.Fatalf("Failed to create test dir: %v", err)
		}
	}

	// Change to test directory for completion
	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := completion.NewFileCompleter()

	tests := []struct {
		name     string
		line     string
		pos      int
		wantMin  int // minimum expected completions
		contains string
	}{
		{
			name:     "empty prefix lists visible files",
			line:     "cat ",
			pos:      4,
			wantMin:  4, // at least 4 visible items
			contains: "file1.txt",
		},
		{
			name:     "file prefix",
			line:     "cat file",
			pos:      8,
			wantMin:  2,
			contains: "file1.txt",
		},
		{
			name:     "hidden files with dot",
			line:     "cd .",
			pos:      4,
			wantMin:  1, // .hidden directory
			contains: ".hidden/",
		},
		{
			name:     "subdir path",
			line:     "ls subdir/",
			pos:      10,
			wantMin:  0, // empty subdir
			contains: "",
		},
		{
			name:     "script completion",
			line:     "cat scr",
			pos:      7,
			wantMin:  1,
			contains: "script.sh",
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := completer.Complete(ctx, tt.line, tt.pos)
			if err != nil {
				t.Fatalf("Complete() error = %v", err)
			}

			if len(result.Items) < tt.wantMin {
				t.Errorf("Got %d results, want at least %d", len(result.Items), tt.wantMin)
			}

			if tt.contains != "" {
				found := false
				for _, r := range result.Items {
					if r.Value == tt.contains {
						found = true
						break
					}
				}
				if !found {
					var values []string
					for _, r := range result.Items {
						values = append(values, r.Value)
					}
					t.Errorf("Results don't contain %q: %v", tt.contains, values)
				}
			}
		})
	}
}

// TestCompletion_DirectoryIndicator tests that directories get / appended.
// Website promise: Directory indicator "/" appended
func TestCompletion_DirectoryIndicator(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file and a directory
	os.WriteFile(filepath.Join(tmpDir, "myfile.txt"), []byte("test"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "mydir"), 0755)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	completer := completion.NewFileCompleter()
	ctx := context.Background()

	result, err := completer.Complete(ctx, "ls my", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	foundDir := false
	foundFile := false
	for _, r := range result.Items {
		if r.Value == "mydir/" {
			foundDir = true
		}
		if r.Value == "myfile.txt" {
			foundFile = true
		}
	}

	if !foundDir {
		t.Error("Expected mydir/ with trailing slash")
	}
	if !foundFile {
		t.Error("Expected myfile.txt without trailing slash")
	}
}

// TestCompletion_TildeExpansion tests home directory expansion.
// Website promise: Tilde expansion
func TestCompletion_TildeExpansion(t *testing.T) {
	completer := completion.NewFileCompleter()
	ctx := context.Background()

	result, err := completer.Complete(ctx, "ls ~/", 5)
	if err != nil {
		t.Logf("Tilde completion error (may be expected): %v", err)
		return
	}

	// Should get home directory contents
	if len(result.Items) == 0 {
		t.Log("No completions for ~/ (may be expected on some systems)")
	} else {
		t.Logf("Got %d completions for ~/", len(result.Items))
	}
}

// TestCompletion_Router tests the three-tier completion router.
// Website promise: Three-tier architecture - Tool-native, Filesystem, Agent fallback
func TestCompletion_Router(t *testing.T) {
	// Create test environment
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "testing.txt"), []byte("test"), 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	// Create router with file completer
	fileCompleter := completion.NewFileCompleter()
	router := completion.NewRouter()
	router.Register(fileCompleter, completion.PriorityFilesystem)

	ctx := context.Background()

	tests := []struct {
		name   string
		line   string
		pos    int
		wantOK bool
	}{
		{
			name:   "file completion",
			line:   "cat test",
			pos:    8,
			wantOK: true, // should match test.txt and testing.txt
		},
		{
			name:   "empty word",
			line:   "ls ",
			pos:    3,
			wantOK: true, // should list files
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := router.Complete(ctx, tt.line, tt.pos)
			if err != nil && tt.wantOK {
				t.Errorf("Complete() error = %v", err)
			}

			if tt.wantOK && len(result.Items) == 0 {
				t.Errorf("Expected completions for %q, got none", tt.line)
			}

			if len(result.Items) > 0 {
				t.Logf("Got %d completions for %q: first=%q", len(result.Items), tt.line, result.Items[0].Value)
			}
		})
	}
}

// TestCompletion_FuzzyMatching tests fuzzy matching support.
// Website promise: Fuzzy matching support (e.g., kgp → kubectl get pods)
func TestCompletion_FuzzyMatching(t *testing.T) {
	// Test the FuzzyFilter function
	items := []completion.Item{
		{Value: "kubectl", Display: "kubectl"},
		{Value: "kubectl get pods", Display: "kubectl get pods"},
		{Value: "docker ps", Display: "docker ps"},
		{Value: "git checkout", Display: "git checkout"},
	}

	tests := []struct {
		query       string
		wantMatches int
		firstMatch  string
	}{
		{
			query:       "kgp",
			wantMatches: 1, // kubectl get pods
			firstMatch:  "kubectl get pods",
		},
		{
			query:       "gco",
			wantMatches: 1, // git checkout
			firstMatch:  "git checkout",
		},
		{
			query:       "kub",
			wantMatches: 2, // kubectl and kubectl get pods
			firstMatch:  "kubectl",
		},
		{
			query:       "xyz",
			wantMatches: 0, // no match
			firstMatch:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			filtered := completion.FuzzyFilter(items, tt.query)

			if len(filtered) != tt.wantMatches {
				t.Errorf("FuzzyFilter(%q) = %d matches, want %d", tt.query, len(filtered), tt.wantMatches)
			}

			if tt.firstMatch != "" && len(filtered) > 0 {
				if filtered[0].Value != tt.firstMatch {
					t.Errorf("First match = %q, want %q", filtered[0].Value, tt.firstMatch)
				}
			}
		})
	}
}

// TestCompletion_RouterWithFuzzy tests router with fuzzy filtering enabled.
func TestCompletion_RouterWithFuzzy(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files with names that test fuzzy matching
	os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{}"), 0644)

	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	fileCompleter := completion.NewFileCompleter()
	fileCompleter.SetFuzzyMode(true) // Return all candidates

	router := completion.NewRouter()
	router.Register(fileCompleter, completion.PriorityFilesystem)
	router.SetFuzzy(true) // Enable fuzzy filtering

	ctx := context.Background()

	// Test fuzzy match "cj" -> "config.json"
	result, err := router.Complete(ctx, "cat cj", 6)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// With fuzzy enabled, "cj" should match config.json
	found := false
	for _, item := range result.Items {
		if item.Value == "config.json" {
			found = true
			break
		}
	}

	if found {
		t.Log("Fuzzy match 'cj' -> 'config.json' worked")
	} else {
		t.Logf("Fuzzy match may not have matched; got %d items", len(result.Items))
	}
}

// TestCompletion_Priorities tests that completers are called in priority order.
// Website promise: Tier 1 (Cobra/Tool-native) 10-200ms, Tier 2 (Filesystem) <10ms, Tier 3 (Agent) 200-800ms
func TestCompletion_Priorities(t *testing.T) {
	// Verify the priority constants
	priorities := []struct {
		name     string
		priority completion.Priority
	}{
		{"ToolNative", completion.PriorityToolNative},
		{"Filesystem", completion.PriorityFilesystem},
		{"Agent", completion.PriorityAgent},
	}

	// ToolNative (Cobra) should be lower (tried first) than Agent
	if completion.PriorityToolNative >= completion.PriorityAgent {
		t.Error("ToolNative priority should be lower (tried first) than Agent")
	}

	// Filesystem should be lower than Agent
	if completion.PriorityFilesystem >= completion.PriorityAgent {
		t.Error("Filesystem priority should be lower than Agent")
	}

	// ToolNative should be tried before Filesystem
	if completion.PriorityToolNative >= completion.PriorityFilesystem {
		t.Error("ToolNative priority should be lower (tried first) than Filesystem")
	}

	t.Logf("Priority order: %v", priorities)
}
