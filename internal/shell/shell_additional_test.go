package shell

import (
	"bytes"
	"strings"
	"testing"
)

// TestDedupe tests the dedupe function.
func TestDedupe(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"empty", nil, nil},
		{"single", []string{"a"}, []string{"a"}},
		{"no duplicates", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"all duplicates", []string{"a", "a", "a"}, []string{"a"}},
		{"mixed", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{"preserve order", []string{"c", "b", "a", "b", "c"}, []string{"c", "b", "a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupe(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("dedupe(%v) = %v, want %v", tt.input, got, tt.expected)
				return
			}
			for i, v := range got {
				if v != tt.expected[i] {
					t.Errorf("dedupe(%v)[%d] = %q, want %q", tt.input, i, v, tt.expected[i])
				}
			}
		})
	}
}

// TestFindSimilar_EdgeCases tests edge cases for findSimilar.
func TestFindSimilar_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		cmd        string
		candidates []string
		maxResults int
		wantLen    int
	}{
		{"empty candidates", "ls", nil, 3, 0},
		{"exact match excluded", "ls", []string{"ls"}, 3, 0},
		{"max results limit", "g", []string{"a", "b", "c", "d", "e"}, 2, 2},
		{"unicode", "猫", []string{"狗", "猫咪"}, 3, 0}, // No matches within distance
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findSimilar(tt.cmd, tt.candidates, tt.maxResults)
			if len(got) != tt.wantLen {
				t.Errorf("findSimilar(%q) got %d results, want %d", tt.cmd, len(got), tt.wantLen)
			}
		})
	}
}

// TestDamerauLevenshtein_EdgeCases tests edge cases for the distance function.
func TestDamerauLevenshtein_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{"both empty", "", "", 0},
		{"unicode same", "日本", "日本", 0},
		{"unicode different", "日本", "中国", 6}, // bytes, not runes
		{"long strings", "abcdefghij", "abcdefghij", 0},
		{"completely different", "abc", "xyz", 3},
		{"case sensitive", "ABC", "abc", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := damerauLevenshtein(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("damerauLevenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestErrorHandler_HandleCommandNotFound_AllCases tests command not found formatting.
func TestErrorHandler_HandleCommandNotFound_AllCases(t *testing.T) {
	tests := []struct {
		name        string
		cmd         string
		suggestions []string
		installHint string
		wantContain []string
	}{
		{
			name:        "basic",
			cmd:         "gti",
			suggestions: nil,
			installHint: "",
			wantContain: []string{"gti", "command not found"},
		},
		{
			name:        "with suggestions",
			cmd:         "gti",
			suggestions: []string{"git"},
			installHint: "",
			wantContain: []string{"gti", "did you mean", "git"},
		},
		{
			name:        "with install hint",
			cmd:         "jq",
			suggestions: nil,
			installHint: "brew install jq",
			wantContain: []string{"jq", "install", "brew install jq"},
		},
		{
			name:        "all fields",
			cmd:         "rg",
			suggestions: []string{"grep"},
			installHint: "brew install ripgrep",
			wantContain: []string{"rg", "did you mean", "grep", "install", "ripgrep"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := &ErrorHandler{out: &buf}
			h.HandleCommandNotFound(tt.cmd, tt.suggestions, tt.installHint)

			output := buf.String()
			for _, want := range tt.wantContain {
				if !strings.Contains(output, want) {
					t.Errorf("output should contain %q, got:\n%s", want, output)
				}
			}
		})
	}
}

// TestErrorHandler_GetSuggestion tests getting fix suggestions.
func TestErrorHandler_GetSuggestion(t *testing.T) {
	// Test with nil fix store
	h := &ErrorHandler{fixStore: nil}

	// Should return empty for exit code 0
	got := h.GetSuggestion("ls", "", 0)
	if got != "" {
		t.Errorf("GetSuggestion with exit 0 should be empty, got %q", got)
	}

	// Should return empty with nil store
	got = h.GetSuggestion("ls", "error", 1)
	if got != "" {
		t.Errorf("GetSuggestion with nil store should be empty, got %q", got)
	}
}

// TestErrorHandler_FormatErrorPrompt tests error prompt formatting.
func TestErrorHandler_FormatErrorPrompt(t *testing.T) {
	h := &ErrorHandler{}

	tests := []struct {
		exitCode int
		stderr   string
		want     string
	}{
		{1, "error", "x Exit 1 | ?? to explain"},
		{127, "command not found", "x Exit 127 | ?? to explain"},
		{255, "", "x Exit 255 | ?? to explain"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := h.FormatErrorPrompt(tt.exitCode, tt.stderr)
			if got != tt.want {
				t.Errorf("FormatErrorPrompt(%d, %q) = %q, want %q",
					tt.exitCode, tt.stderr, got, tt.want)
			}
		})
	}
}

// TestErrorHandler_HandleError_ExitZero tests that exit 0 is ignored.
func TestErrorHandler_HandleError_ExitZero(t *testing.T) {
	var buf bytes.Buffer
	h := &ErrorHandler{out: &buf}

	h.HandleError("ls", "", 0)

	if buf.Len() > 0 {
		t.Errorf("HandleError with exit 0 should produce no output, got: %s", buf.String())
	}
}

// TestNewErrorHandler tests error handler creation.
func TestNewErrorHandler(t *testing.T) {
	h := NewErrorHandler(nil)
	if h.fixStore != nil {
		t.Error("NewErrorHandler with nil should have nil fixStore")
	}
}

// TestCommandSuggestor_Suggest tests the Suggest method.
func TestCommandSuggestor_Suggest(t *testing.T) {
	// Create suggestor without history store
	s := &CommandSuggestor{}

	// Manually set PATH cache
	s.pathCacheMu.Lock()
	s.pathCache = []string{"git", "grep", "go", "gzip"}
	s.pathCacheMu.Unlock()
	s.pathCacheReady.Store(true)

	tests := []struct {
		name    string
		cmd     string
		wantAny []string // Any of these should be in result
	}{
		{"typo gti", "gti", []string{"git"}},
		{"typo grpe", "grpe", []string{"grep"}},
		{"no match", "zzzzz", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Suggest(tt.cmd)

			if tt.wantAny == nil {
				if len(got) > 0 {
					t.Errorf("Suggest(%q) = %v, want empty", tt.cmd, got)
				}
				return
			}

			found := false
			for _, want := range tt.wantAny {
				for _, g := range got {
					if g == want {
						found = true
						break
					}
				}
			}
			if !found {
				t.Errorf("Suggest(%q) = %v, want any of %v", tt.cmd, got, tt.wantAny)
			}
		})
	}
}

// TestInstallHint_UnknownPackageManager tests unknown package manager.
func TestInstallHint_UnknownPackageManager(t *testing.T) {
	s := &CommandSuggestor{}
	// Trigger pmOnce with "unknown" value
	s.pmOnce.Do(func() {
		s.packageManager = "unknown"
	})

	hint := s.InstallHint("jq")
	if hint != "" {
		t.Errorf("InstallHint with unknown pm should be empty, got %q", hint)
	}
}

// TestInstallHint_UnknownCommand tests unknown command.
func TestInstallHint_UnknownCommand(t *testing.T) {
	s := &CommandSuggestor{packageManager: "brew"}

	hint := s.InstallHint("totally_unknown_command_xyz")
	if hint != "" {
		t.Errorf("InstallHint for unknown command should be empty, got %q", hint)
	}
}
