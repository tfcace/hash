package allowlist

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	projectDir := t.TempDir()
	configDir := t.TempDir()
	m := New("project", projectDir, configDir)

	// Allow a command
	if err := m.Allow("git status"); err != nil {
		t.Fatalf("Allow() error: %v", err)
	}

	// Verify file was created outside the project worktree
	filePath := filepath.Join(configDir, "project_allowlists", projectScopeKey(projectDir)+".json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("allowlist file should be created")
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".hash", "allowed_commands.json")); !os.IsNotExist(err) {
		t.Error("project scope should not create repo-local allowlist files")
	}

	// Create new manager, should load from file
	m2 := New("project", projectDir, configDir)
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

func TestProjectScopeKey_UsesCanonicalPath(t *testing.T) {
	projectDir := t.TempDir()
	linkDir := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(projectDir, linkDir); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	if got, want := projectScopeKey(linkDir), projectScopeKey(projectDir); got != want {
		t.Fatalf("projectScopeKey() should be stable across symlinks: got %q want %q", got, want)
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

func TestManager_SetProjectDirReloadsCommands(t *testing.T) {
	projectA := t.TempDir()
	projectB := t.TempDir()
	configDir := t.TempDir()

	mA := New("project", projectA, configDir)
	if err := mA.Allow("git status"); err != nil {
		t.Fatalf("Allow(projectA): %v", err)
	}

	mB := New("project", projectB, configDir)
	if err := mB.Allow("npm test"); err != nil {
		t.Fatalf("Allow(projectB): %v", err)
	}

	m := New("project", projectA, configDir)
	if !m.IsAllowed("git status") {
		t.Fatal("expected projectA allowlist to be loaded")
	}

	if err := m.SetProjectDir(projectB); err != nil {
		t.Fatalf("SetProjectDir(projectB): %v", err)
	}

	if m.IsAllowed("git status") {
		t.Fatal("projectA commands should not remain allowed after switching projects")
	}
	if !m.IsAllowed("npm test") {
		t.Fatal("expected projectB allowlist to be loaded after switching projects")
	}

	if err := m.SetProjectDir(projectA); err != nil {
		t.Fatalf("SetProjectDir(projectA): %v", err)
	}

	if !m.IsAllowed("git status") {
		t.Fatal("expected projectA allowlist to reload when switching back")
	}
	if m.IsAllowed("npm test") {
		t.Fatal("projectB commands should not remain allowed after switching back")
	}
}

// A scope that persists but has nowhere to write says why instead of
// silently keeping approvals in memory; session scope has no file by design.
func TestManager_NoFileReason(t *testing.T) {
	tests := []struct {
		name    string
		m       *Manager
		wantErr error
	}{
		{"session", New(ScopeSession, "/p", "/cfg"), nil},
		{"global without config dir", New(ScopeGlobal, "/p", ""), errNoConfigDir},
		{"project without config dir", New(ScopeProject, "/p", ""), errNoConfigDir},
		{"project without project dir", New(ScopeProject, "", t.TempDir()), errNoProjectDir},
	}
	for _, tt := range tests {
		if err := tt.m.Allow("ls"); !errors.Is(err, tt.wantErr) {
			t.Errorf("%s: Allow() = %v, want %v", tt.name, err, tt.wantErr)
		}
		if !tt.m.IsAllowed("ls") {
			t.Errorf("%s: approval not kept for the session", tt.name)
		}
	}
}

// An approval moves: session scope keeps it in memory for good; a file scope
// keeps it unsaved while saving fails, retries it with the next approval,
// and forgets it on a project switch.
func TestManager_ApprovalTransitions(t *testing.T) {
	session := New(ScopeSession, "", t.TempDir())
	if err := session.Allow("ls"); err != nil || !session.IsAllowed("ls") || len(session.unsaved) != 0 {
		t.Fatalf("session: err = %v allowed = %v unsaved = %v", err, session.IsAllowed("ls"), session.unsaved)
	}

	cfg := t.TempDir()
	m := New(ScopeProject, "/p1", cfg)
	blocked := filepath.Join(cfg, "project_allowlists")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Allow("make"); err == nil || !m.IsAllowed("make") || !m.unsaved["make"] {
		t.Fatalf("failed save: err = %v allowed = %v unsaved = %v", err, m.IsAllowed("make"), m.unsaved)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := m.Allow("go test"); err != nil || len(m.unsaved) != 0 {
		t.Fatalf("retry: err = %v unsaved = %v", err, m.unsaved)
	}
	if cmds, _ := readFile(filepath.Join(blocked, projectScopeKey("/p1")+".json")); !slices.Contains(cmds, "make") {
		t.Errorf("the failed approval was not saved with the next one: %v", cmds)
	}

	m2 := New(ScopeProject, "", cfg)
	if err := m2.Allow("rm"); err == nil || !m2.unsaved["rm"] {
		t.Fatalf("no project dir: err = %v unsaved = %v", err, m2.unsaved)
	}
	_ = m2.SetProjectDir("/p2")
	if m2.IsAllowed("rm") || len(m2.unsaved) != 0 {
		t.Errorf("project switch kept the old project's unsaved approval")
	}
}
