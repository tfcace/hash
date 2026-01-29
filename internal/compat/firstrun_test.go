// internal/compat/firstrun_test.go
package compat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsFirstRun_NoHashrc(t *testing.T) {
	tmpDir := t.TempDir()
	home := tmpDir
	dataDir := filepath.Join(tmpDir, ".local", "share", "hash")

	// No .hashrc, no migration.json -> first run
	isFirst, _ := isFirstRun(home, dataDir)
	if !isFirst {
		t.Error("expected first run when no .hashrc exists")
	}
}

func TestIsFirstRun_HasHashrc(t *testing.T) {
	tmpDir := t.TempDir()
	home := tmpDir
	dataDir := filepath.Join(tmpDir, ".local", "share", "hash")

	// Create .hashrc
	os.WriteFile(filepath.Join(home, ".hashrc"), []byte("# config"), 0644)

	isFirst, _ := isFirstRun(home, dataDir)
	if isFirst {
		t.Error("expected not first run when .hashrc exists")
	}
}

func TestIsFirstRun_HasMigrationState(t *testing.T) {
	tmpDir := t.TempDir()
	home := tmpDir
	dataDir := filepath.Join(tmpDir, ".local", "share", "hash")

	// Create migration.json (user declined before)
	os.MkdirAll(dataDir, 0755)
	state := &State{Declined: true}
	state.Save(filepath.Join(dataDir, "migration.json"))

	isFirst, _ := isFirstRun(home, dataDir)
	if isFirst {
		t.Error("expected not first run when migration state exists")
	}
}
