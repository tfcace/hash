package shell

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
)

func TestNewShell(t *testing.T) {
	cfg := config.Default()

	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if sh == nil {
		t.Error("New() returned nil")
	}
}

func TestShell_ChpwdHook(t *testing.T) {
	// Save and restore working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	tmpDirResolved, _ := filepath.EvalSymlinks(tmpDir)

	// Create a marker file path for the hook to write to
	markerFile := filepath.Join(tmpDir, "chpwd-marker")

	cfg := config.Default()
	cfg.Shell.Hooks.Chpwd = []string{
		`echo "$PWD" >> ` + markerFile,
	}

	// Create a minimal shell-like setup to test runChpwdHook
	e := executor.New()

	sh := &Shell{
		config:   cfg,
		executor: e,
		prevCwd:  origDir,
	}

	ctx := context.Background()

	// Before cd: hook should not fire (same dir)
	sh.runChpwdHook(ctx)
	if _, err := os.Stat(markerFile); err == nil {
		t.Error("chpwd hook should not fire when directory hasn't changed")
	}

	// Change directory
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	e.SetExportedEnv("PWD", tmpDir)
	e.SyncRunnerDir()

	// Now hook should fire
	sh.runChpwdHook(ctx)

	content, err := os.ReadFile(markerFile)
	if err != nil {
		t.Fatalf("marker file should exist after chpwd: %v", err)
	}

	got := strings.TrimSpace(string(content))
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != tmpDirResolved {
		t.Errorf("chpwd hook recorded %q, want %q", got, tmpDirResolved)
	}

	// Running hook again without directory change should NOT append
	sh.runChpwdHook(ctx)
	content2, _ := os.ReadFile(markerFile)
	lines1 := strings.Count(strings.TrimSpace(string(content)), "\n") + 1
	lines2 := strings.Count(strings.TrimSpace(string(content2)), "\n") + 1
	if lines2 != lines1 {
		t.Errorf("chpwd hook should not fire again when directory hasn't changed (got %d lines, want %d)", lines2, lines1)
	}
}

func TestShell_ChpwdHook_NoConfig(t *testing.T) {
	cfg := config.Default()
	// No hooks configured - should be a no-op
	sh := &Shell{
		config:   cfg,
		executor: executor.New(),
		prevCwd:  "/tmp",
	}

	// Should not panic
	sh.runChpwdHook(context.Background())
}

func TestShell_ZoxideWorksWithDisabledCdBuiltin(t *testing.T) {
	if _, err := exec.LookPath("zoxide"); err != nil {
		t.Skip("zoxide not installed")
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer os.Chdir(origDir) //nolint:errcheck

	cfg := config.Default()
	cfg.Shell.DisableBuiltins = []string{"cd"}

	e := executor.New()
	sh := &Shell{
		config:   cfg,
		executor: e,
	}

	ctx := context.Background()
	_, err = e.Execute(ctx, `eval "$(zoxide init bash)"`, nil, nil)
	if err != nil {
		t.Fatalf("failed to initialize zoxide: %v", err)
	}

	if err := sh.executeRegularCommand(ctx, "z ~"); err != nil {
		t.Fatalf("z command failed: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd after z: %v", err)
	}
	cwdResolved, _ := filepath.EvalSymlinks(cwd)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get home: %v", err)
	}
	homeResolved, _ := filepath.EvalSymlinks(home)

	if cwdResolved != homeResolved {
		t.Fatalf("z should change to home: got %q, want %q", cwdResolved, homeResolved)
	}
}

func TestShell_ModeMarkers(t *testing.T) {
	// Clear markers first
	os.Unsetenv("HASH_LOGIN")
	os.Unsetenv("HASH_INTERACTIVE")

	cfg := config.Default()
	mode := Mode{Login: true, Interactive: true}

	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	// Check that mode is stored
	if !sh.mode.Login {
		t.Error("expected Login mode to be true")
	}
	if !sh.mode.Interactive {
		t.Error("expected Interactive mode to be true")
	}
}
