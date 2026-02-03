package allowlist

import (
	"os"
	"path/filepath"
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
