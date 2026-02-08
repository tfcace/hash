package allowlist

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManager_SessionScope(t *testing.T) {
	m := New("session", "", "")

	// Initially not allowed
	if m.IsAllowed("kubectl get pods") {
		t.Error("command should not be allowed initially")
	}

	// Allow it
	if err := m.Allow("kubectl get pods"); err != nil {
		t.Fatalf("Allow() error: %v", err)
	}

	// Now it should be allowed
	if !m.IsAllowed("kubectl get pods") {
		t.Error("command should be allowed after Allow()")
	}

	// Different command still not allowed
	if m.IsAllowed("kubectl delete pods") {
		t.Error("different command should not be allowed")
	}
}

func TestManager_ProjectScope(t *testing.T) {
	tmpDir := t.TempDir()
	m := New("project", tmpDir, "")

	// Allow a command
	if err := m.Allow("git status"); err != nil {
		t.Fatalf("Allow() error: %v", err)
	}

	// Verify file was created
	filePath := filepath.Join(tmpDir, ".hash", "allowed_commands.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("allowlist file should be created")
	}

	// Create new manager, should load from file
	m2 := New("project", tmpDir, "")
	if err := m2.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !m2.IsAllowed("git status") {
		t.Error("command should persist across manager instances")
	}
}

func TestManager_GlobalScope(t *testing.T) {
	tmpDir := t.TempDir()
	m := New("global", "", tmpDir)

	if err := m.Allow("ls -la"); err != nil {
		t.Fatalf("Allow() error: %v", err)
	}

	// Verify file was created in global dir
	filePath := filepath.Join(tmpDir, "allowed_commands.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("global allowlist file should be created")
	}
}

func TestManager_ProjectScope_RefusesGitTracked(t *testing.T) {
	// Set up a temporary git repo with a tracked allowlist file
	tmpDir := t.TempDir()
	hashDir := filepath.Join(tmpDir, ".hash")
	if err := os.MkdirAll(hashDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write an allowlist file
	allowFile := filepath.Join(hashDir, "allowed_commands.json")
	if err := os.WriteFile(allowFile, []byte(`{"allowed_commands":["rm -rf /"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Initialize a git repo and track the file
	cmds := []struct{ args []string }{
		{[]string{"git", "init"}},
		{[]string{"git", "config", "user.email", "test@test.com"}},
		{[]string{"git", "config", "user.name", "Test"}},
		{[]string{"git", "add", ".hash/allowed_commands.json"}},
		{[]string{"git", "commit", "-m", "init"}},
	}
	for _, c := range cmds {
		cmd := exec.Command(c.args[0], c.args[1:]...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", c.args, err, out)
		}
	}

	// Create manager — should refuse to load the tracked file
	m := New("project", tmpDir, "")
	if m.IsAllowed("rm -rf /") {
		t.Error("should not load allowlist from git-tracked file")
	}
}

func TestManager_ProjectScope_RefusesWhenGitUnavailable(t *testing.T) {
	// Set up a temporary git repo with a tracked allowlist file.
	tmpDir := t.TempDir()
	hashDir := filepath.Join(tmpDir, ".hash")
	if err := os.MkdirAll(hashDir, 0o755); err != nil {
		t.Fatal(err)
	}

	allowFile := filepath.Join(hashDir, "allowed_commands.json")
	if err := os.WriteFile(allowFile, []byte(`{"allowed_commands":["rm -rf /"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmds := []struct{ args []string }{
		{[]string{"git", "init"}},
		{[]string{"git", "config", "user.email", "test@test.com"}},
		{[]string{"git", "config", "user.name", "Test"}},
		{[]string{"git", "add", ".hash/allowed_commands.json"}},
		{[]string{"git", "commit", "-m", "init"}},
	}
	for _, c := range cmds {
		cmd := exec.Command(c.args[0], c.args[1:]...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", c.args, err, out)
		}
	}

	// Hide git from PATH so tracking cannot be verified.
	t.Setenv("PATH", t.TempDir())

	m := New("project", tmpDir, "")

	// Explicit Load should fail closed when git lookup cannot run.
	err := m.Load()
	if err == nil {
		t.Fatal("expected Load() to fail when git is unavailable")
	}
	if !strings.Contains(err.Error(), "unable to verify git tracking") {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.IsAllowed("rm -rf /") {
		t.Error("should not load allowlist when git tracking cannot be verified")
	}
}

func TestManager_ProjectScope_LoadsUntrackedFileInGitRepo(t *testing.T) {
	// Set up a git repo where allowlist file exists but is not tracked.
	tmpDir := t.TempDir()
	hashDir := filepath.Join(tmpDir, ".hash")
	if err := os.MkdirAll(hashDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	allowFile := filepath.Join(hashDir, "allowed_commands.json")
	if err := os.WriteFile(allowFile, []byte(`{"allowed_commands":["git status"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmds := []struct{ args []string }{
		{[]string{"git", "init"}},
		{[]string{"git", "config", "user.email", "test@test.com"}},
		{[]string{"git", "config", "user.name", "Test"}},
		{[]string{"git", "add", "README.md"}},
		{[]string{"git", "commit", "-m", "init"}},
	}
	for _, c := range cmds {
		cmd := exec.Command(c.args[0], c.args[1:]...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", c.args, err, out)
		}
	}

	m := New("project", tmpDir, "")
	if !m.IsAllowed("git status") {
		t.Error("should load commands from untracked allowlist file in git repo")
	}
}

func TestManager_ExactMatch(t *testing.T) {
	m := New("session", "", "")
	m.Allow("kubectl get pods")

	// Exact match works
	if !m.IsAllowed("kubectl get pods") {
		t.Error("exact match should work")
	}

	// Substring doesn't match
	if m.IsAllowed("kubectl get pods -n default") {
		t.Error("substring should not match")
	}

	// Prefix doesn't match
	if m.IsAllowed("kubectl get") {
		t.Error("prefix should not match")
	}
}
