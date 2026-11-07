package allowlist

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Two shells in the same project each approve a command; neither approval
// may be lost when the other saves.
func TestManager_SaveMergesOtherShellsApprovals(t *testing.T) {
	projectDir, configDir := t.TempDir(), t.TempDir()
	a := New("project", projectDir, configDir)
	b := New("project", projectDir, configDir)

	if err := a.Allow("git status"); err != nil {
		t.Fatal(err)
	}
	if err := b.Allow("go test ./..."); err != nil {
		t.Fatal(err)
	}

	c := New("project", projectDir, configDir)
	for _, cmd := range []string{"git status", "go test ./..."} {
		if !c.IsAllowed(cmd) {
			t.Errorf("%q was lost", cmd)
		}
	}
}

// A corrupt allowlist file is kept aside, not silently overwritten by the
// next approval.
func TestManager_CorruptFileIsBackedUp(t *testing.T) {
	projectDir, configDir := t.TempDir(), t.TempDir()
	path, _ := New(ScopeProject, projectDir, configDir).filePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"allowed_commands": ["git st`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := New("project", projectDir, configDir)
	if err := m.Allow("git status"); err != nil {
		t.Fatal(err)
	}

	backup, err := os.ReadFile(path + ".corrupt")
	if err != nil {
		t.Fatalf("corrupt allowlist was not backed up: %v", err)
	}
	if string(backup) != `{"allowed_commands": ["git st` {
		t.Errorf("backup = %q, want the corrupt content", backup)
	}
	if !New("project", projectDir, configDir).IsAllowed("git status") {
		t.Error("new approval was not saved")
	}
}

// cd (SetProjectDir) and an approval (Allow, from the agent's permission
// handler) can run at once.
func TestManager_SetProjectDirWhileAllowing(t *testing.T) {
	configDir := t.TempDir()
	dirs := []string{t.TempDir(), t.TempDir()}
	m := New("project", dirs[0], configDir)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = m.SetProjectDir(dirs[i%2])
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = m.Allow("echo hi")
		}
	}()
	wg.Wait()
}

func TestParseScope(t *testing.T) {
	for in, want := range map[string]Scope{"project": ScopeProject, "global": ScopeGlobal, "session": ScopeSession, "": ScopeProject} {
		got, err := ParseScope(in)
		if err != nil || got != want {
			t.Errorf("ParseScope(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	// A typo used to behave silently as session scope: approvals vanished on exit.
	if got, err := ParseScope("projcet"); err == nil || got != ScopeSession {
		t.Errorf("ParseScope(projcet) = %q, %v, want session and an error", got, err)
	}
}

// Deleting an approval from the file revokes it. Saving used to merge every
// command a shell had in memory back in, so the next Allow from any shell
// that loaded it earlier (or the shell that approved it) restored it.
func TestManager_ApprovalDeletedFromFileStaysRevoked(t *testing.T) {
	projectDir, configDir := t.TempDir(), t.TempDir()
	a := New(ScopeProject, projectDir, configDir)
	if err := a.Allow("rm -rf build"); err != nil {
		t.Fatal(err)
	}
	b := New(ScopeProject, projectDir, configDir) // loads rm -rf build

	path, _ := a.filePath()
	if err := os.WriteFile(path, []byte(`{"allowed_commands": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Allow("git status"); err != nil {
		t.Fatal(err)
	}
	if err := a.Allow("ls"); err != nil {
		t.Fatal(err)
	}

	for name, m := range map[string]*Manager{"approving shell": a, "other shell": b, "new shell": New(ScopeProject, projectDir, configDir)} {
		if m.IsAllowed("rm -rf build") {
			t.Errorf("%s: revoked approval is allowed again", name)
		}
		if !m.IsAllowed("git status") || !m.IsAllowed("ls") {
			t.Errorf("%s: lost an approval made after the revocation", name)
		}
	}
}
