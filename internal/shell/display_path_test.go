package shell

import (
	"path/filepath"
	"strings"
	"testing"
)

// A directory beside home that starts with home's name is not under it:
// /Users/al must not turn /Users/alice/x into ~ice/x.
func TestDisplayPaths_HomePrefixIsNotHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "al"))
	t.Setenv("HASH_CONFIG_DIR", filepath.Join(root, "alice", "cfg"))
	var s Shell
	if got := s.shortenPath(filepath.Join(root, "alice", "x")); strings.Contains(got, "~") {
		t.Errorf("shortenPath = %q", got)
	}
	if got := completionsSpecsDirDisplay(); strings.Contains(got, "~") {
		t.Errorf("completionsSpecsDirDisplay = %q", got)
	}
	t.Setenv("HASH_CONFIG_DIR", filepath.Join(root, "al", "cfg"))
	if got := completionsSpecsDirDisplay(); got != "~/cfg/completions" {
		t.Errorf("completionsSpecsDirDisplay = %q, want ~/cfg/completions", got)
	}
}
