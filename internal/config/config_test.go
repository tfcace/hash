package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_DefaultValues(t *testing.T) {
	// Create temp dir with no config file
	tmpDir := t.TempDir()

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Check defaults
	if cfg.Shell.Keybindings != "emacs" {
		t.Errorf("Keybindings = %q, want %q", cfg.Shell.Keybindings, "emacs")
	}
	if cfg.Prompt.Mode != "starship" {
		t.Errorf("Prompt.Mode = %q, want %q", cfg.Prompt.Mode, "starship")
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[shell]
keybindings = "vim"
editor = "nvim"

[prompt]
mode = "built-in"
`)
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Shell.Keybindings != "vim" {
		t.Errorf("Keybindings = %q, want %q", cfg.Shell.Keybindings, "vim")
	}
	if cfg.Shell.Editor != "nvim" {
		t.Errorf("Editor = %q, want %q", cfg.Shell.Editor, "nvim")
	}
	if cfg.Prompt.Mode != "built-in" {
		t.Errorf("Prompt.Mode = %q, want %q", cfg.Prompt.Mode, "built-in")
	}
}

func TestConfig_StartupFiles(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
[shell]
profile = [
    "export FOO=bar",
]
rc_commands = [
    "alias ll='ls -la'",
]

[shell.startup_files]
login = [
    "/etc/profile",
    "~/.profile",
    "~/.hash_profile",
]
interactive = [
    "~/.hashrc",
]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Check profile commands
	if len(cfg.Shell.ProfileCommands) != 1 {
		t.Errorf("expected 1 profile command, got %d", len(cfg.Shell.ProfileCommands))
	}
	if len(cfg.Shell.RCCommands) != 1 {
		t.Errorf("expected 1 rc command, got %d", len(cfg.Shell.RCCommands))
	}

	// Check startup files
	if len(cfg.Shell.StartupFiles.Login) != 3 {
		t.Errorf("expected 3 login files, got %d", len(cfg.Shell.StartupFiles.Login))
	}
	if len(cfg.Shell.StartupFiles.Interactive) != 1 {
		t.Errorf("expected 1 interactive file, got %d", len(cfg.Shell.StartupFiles.Interactive))
	}
}

func TestConfig_StartupFilesDefaults(t *testing.T) {
	cfg := Default()

	// Check default startup files
	if len(cfg.Shell.StartupFiles.Login) == 0 {
		t.Error("expected default login startup files")
	}
	if len(cfg.Shell.StartupFiles.Interactive) == 0 {
		t.Error("expected default interactive startup files")
	}
}
