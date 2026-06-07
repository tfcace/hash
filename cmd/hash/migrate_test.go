// cmd/hash/migrate_test.go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateStatus_NoState(t *testing.T) {
	var stdout bytes.Buffer
	err := runMigrateStatus(&stdout, "/nonexistent/migration.json")
	if err == nil {
		t.Error("expected error when no migration state exists")
	}
}

func TestMigrateShellDialectReadsConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HASH_CONFIG_DIR", tmpDir)

	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[shell]\ndialect = \"zsh\"\n"), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if got := migrateShellDialect(); got != "zsh" {
		t.Fatalf("migrateShellDialect() = %q, want zsh", got)
	}
}
