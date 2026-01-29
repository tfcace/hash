package shell

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/compat"
	"github.com/tfcace/hash/internal/config"
)

func TestStartup_LoginShell_SourcesProfileThenRC(t *testing.T) {
	// Clean environment to avoid interference from previous tests or migration
	os.Unsetenv("PROFILE_SOURCED")
	os.Unsetenv("RC_AFTER_PROFILE")

	// Isolate from system state by setting HOME and XDG_DATA_HOME to temp dir
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	os.Setenv("XDG_DATA_HOME", tmpDir)
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Unsetenv("XDG_DATA_HOME")
	}()

	// Create test profile that sets a marker
	profilePath := filepath.Join(tmpDir, "profile")
	if err := os.WriteFile(profilePath, []byte("export PROFILE_SOURCED=1\n"), 0644); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}

	// Create test rc that checks profile was sourced first
	rcPath := filepath.Join(tmpDir, "rc")
	rcContent := `if [ "$PROFILE_SOURCED" = "1" ]; then
    export RC_AFTER_PROFILE=yes
else
    export RC_AFTER_PROFILE=no
fi
`
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("failed to write rc: %v", err)
	}

	cfg := config.Default()
	cfg.Shell.StartupFiles.Login = []string{profilePath}
	cfg.Shell.StartupFiles.Interactive = []string{rcPath}
	cfg.Shell.InitCommands = nil

	mode := Mode{Login: true, Interactive: true}
	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	// Run startup
	ctx := context.Background()
	if err := sh.runStartup(ctx); err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	// Check that rc ran after profile
	if os.Getenv("RC_AFTER_PROFILE") != "yes" {
		t.Errorf("expected RC_AFTER_PROFILE=yes, got '%s'", os.Getenv("RC_AFTER_PROFILE"))
	}
}

func TestStartup_NonLoginInteractive_SkipsProfile(t *testing.T) {
	// Isolate from system state by setting HOME and XDG_DATA_HOME to temp dir
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	os.Setenv("XDG_DATA_HOME", tmpDir)
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Unsetenv("XDG_DATA_HOME")
	}()

	// Clean environment
	os.Unsetenv("PROFILE_RAN")
	os.Unsetenv("RC_RAN")

	// Create profile that would fail if sourced
	profilePath := filepath.Join(tmpDir, "profile")
	if err := os.WriteFile(profilePath, []byte("export PROFILE_RAN=1\n"), 0644); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}

	// Create rc
	rcPath := filepath.Join(tmpDir, "rc")
	if err := os.WriteFile(rcPath, []byte("export RC_RAN=1\n"), 0644); err != nil {
		t.Fatalf("failed to write rc: %v", err)
	}

	cfg := config.Default()
	cfg.Shell.StartupFiles.Login = []string{profilePath}
	cfg.Shell.StartupFiles.Interactive = []string{rcPath}
	cfg.Shell.InitCommands = nil

	mode := Mode{Login: false, Interactive: true}
	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	ctx := context.Background()
	if err := sh.runStartup(ctx); err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	// Profile should NOT have run
	if os.Getenv("PROFILE_RAN") == "1" {
		t.Error("profile should not run for non-login shell")
	}

	// RC should have run
	if os.Getenv("RC_RAN") != "1" {
		t.Error("rc should run for interactive shell")
	}
}

func TestStartup_NonInteractive_SkipsRC(t *testing.T) {
	// Isolate from system state by setting HOME and XDG_DATA_HOME to temp dir
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	os.Setenv("XDG_DATA_HOME", tmpDir)
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Unsetenv("XDG_DATA_HOME")
	}()

	// Clean environment
	os.Unsetenv("RC_RAN")

	rcPath := filepath.Join(tmpDir, "rc")
	if err := os.WriteFile(rcPath, []byte("export RC_RAN=1\n"), 0644); err != nil {
		t.Fatalf("failed to write rc: %v", err)
	}

	cfg := config.Default()
	cfg.Shell.StartupFiles.Login = nil
	cfg.Shell.StartupFiles.Interactive = []string{rcPath}
	cfg.Shell.InitCommands = nil

	mode := Mode{Login: false, Interactive: false}
	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	ctx := context.Background()
	if err := sh.runStartup(ctx); err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	// RC should NOT have run
	if os.Getenv("RC_RAN") == "1" {
		t.Error("rc should not run for non-interactive shell")
	}
}

func TestStartup_FirstRunMigration(t *testing.T) {
	// This test verifies the integration point exists
	// Full first-run testing requires interactive input

	tmpDir := t.TempDir()

	// Create a .zshrc in tmp
	os.WriteFile(filepath.Join(tmpDir, ".zshrc"), []byte("alias ll='ls -la'\n"), 0644)

	// Mock the check - just verify the function exists and is callable
	shouldShow, shell, rcFile := compat.ShouldShowMigrationPrompt()

	// In test environment, this depends on actual home dir state
	// Just verify it doesn't panic
	_ = shouldShow
	_ = shell
	_ = rcFile
}
