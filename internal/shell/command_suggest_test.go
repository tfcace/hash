package shell

import (
	"testing"
)

func TestDamerauLevenshtein(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"a", "a", 0},
		{"ab", "ab", 0},
		{"ab", "ba", 1},    // transposition
		{"sl", "ls", 1},    // transposition
		{"gti", "git", 1},  // transposition
		{"cat", "car", 1},  // substitution
		{"cat", "cats", 1}, // insertion
		{"cats", "cat", 1}, // deletion
		{"kitten", "sitting", 3},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			got := damerauLevenshtein(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("damerauLevenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

func TestMaxDistance(t *testing.T) {
	tests := []struct {
		cmd      string
		expected int
	}{
		{"ls", 1},
		{"cat", 1},
		{"grep", 1},
		{"docker", 2},
		{"kubectl", 2},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			got := maxDistance(tt.cmd)
			if got != tt.expected {
				t.Errorf("maxDistance(%q) = %d, want %d", tt.cmd, got, tt.expected)
			}
		})
	}
}

func TestFindSimilar(t *testing.T) {
	candidates := []string{"ls", "cat", "git", "grep", "docker", "du", "dd", "df"}

	tests := []struct {
		cmd      string
		expected []string
	}{
		{"sl", []string{"ls"}},             // transposition
		{"gti", []string{"git"}},           // transposition
		{"dc", []string{"dd", "df", "du"}}, // all within distance 1
		{"dl", []string{"dd", "df", "du"}}, // multiple matches
		{"xyz", nil},                       // no match
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			got := findSimilar(tt.cmd, candidates, 3)
			if len(got) != len(tt.expected) {
				t.Errorf("findSimilar(%q) = %v, want %v", tt.cmd, got, tt.expected)
				return
			}
			for i, g := range got {
				if g != tt.expected[i] {
					t.Errorf("findSimilar(%q)[%d] = %q, want %q", tt.cmd, i, g, tt.expected[i])
				}
			}
		})
	}
}

func TestInstallHint(t *testing.T) {
	// Create suggestor and trigger the sync.Once with brew as package manager
	s := &CommandSuggestor{}
	s.pmOnce.Do(func() {
		s.packageManager = "brew"
	})

	tests := []struct {
		cmd      string
		expected string
	}{
		{"jq", "brew install jq"},
		{"rg", "brew install ripgrep"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			got := s.InstallHint(tt.cmd)
			if got != tt.expected {
				t.Errorf("InstallHint(%q) = %q, want %q", tt.cmd, got, tt.expected)
			}
		})
	}
}

func TestInstallHint_AllPlatforms(t *testing.T) {
	platforms := []string{"brew", "apt", "dnf", "pacman"}

	for _, pm := range platforms {
		t.Run(pm, func(t *testing.T) {
			s := &CommandSuggestor{packageManager: pm}
			hint := s.InstallHint("jq")
			if hint == "" {
				t.Errorf("InstallHint(jq) with %s = empty, want non-empty", pm)
			}
		})
	}
}
