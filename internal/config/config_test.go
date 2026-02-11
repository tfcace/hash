package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
	if cfg.Agent.ConversationIdleTimeout != "10m" {
		t.Errorf("Agent.ConversationIdleTimeout = %q, want %q", cfg.Agent.ConversationIdleTimeout, "10m")
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
	if err := os.WriteFile(configPath, content, 0o644); err != nil { //nolint:gosec // G306: test file
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
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil { //nolint:gosec // G306: test file
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

func TestConfig_ClipboardMaxOutputSize(t *testing.T) {
	cfg := Default()

	// Default should be 1MB
	size, err := cfg.Clipboard.ParseMaxOutputSize()
	if err != nil {
		t.Fatalf("ParseMaxOutputSize error: %v", err)
	}
	if size != 1024*1024 {
		t.Errorf("Default size = %d, want %d", size, 1024*1024)
	}
}

func TestConfig_ClipboardMaxOutputSizeParsing(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1MB", 1024 * 1024},
		{"5MB", 5 * 1024 * 1024},
		{"500KB", 500 * 1024},
		{"1024", 1024},
	}

	for _, tt := range tests {
		cfg := &ClipboardConfig{MaxOutputSize: tt.input}
		got, err := cfg.ParseMaxOutputSize()
		if err != nil {
			t.Errorf("ParseMaxOutputSize(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseMaxOutputSize(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestLoadConfig_ParseError_ReturnsDefaultsWithError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	// Write invalid TOML
	invalidContent := []byte(`
[shell
keybindings = "vim"
`)
	if err := os.WriteFile(configPath, invalidContent, 0o644); err != nil { //nolint:gosec // G306: test file
		t.Fatal(err)
	}

	cfg, err := Load(tmpDir)

	// Should return an error
	if err == nil {
		t.Error("Load() should return error for invalid TOML")
	}

	// But should also return usable defaults
	if cfg == nil {
		t.Fatal("Load() should return defaults even on parse error")
	}

	// Check that defaults are applied
	if cfg.Shell.Keybindings != "emacs" {
		t.Errorf("Keybindings = %q, want default %q", cfg.Shell.Keybindings, "emacs")
	}
	if cfg.Prompt.Mode != "starship" {
		t.Errorf("Prompt.Mode = %q, want default %q", cfg.Prompt.Mode, "starship")
	}
}

func TestLoadWithWarnings_WritesWarning(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	// Write invalid TOML
	invalidContent := []byte(`invalid toml [[[`)
	if err := os.WriteFile(configPath, invalidContent, 0o644); err != nil { //nolint:gosec // G306: test file
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cfg := LoadWithWarnings(tmpDir, &buf)

	// Should return usable config
	if cfg == nil {
		t.Fatal("LoadWithWarnings() should return config")
	}

	// Should have written a warning
	warning := buf.String()
	if warning == "" {
		t.Error("LoadWithWarnings() should write warning for invalid config")
	}
	if !strings.Contains(warning, "Warning") {
		t.Errorf("Warning should contain 'Warning', got: %q", warning)
	}
}

func TestLoadConfig_HooksChpwd(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[shell.hooks]
chpwd = ["zoxide add -- \"$PWD\"", "echo changed"]
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil { //nolint:gosec // G306: test file
		t.Fatal(err)
	}

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(cfg.Shell.Hooks.Chpwd) != 2 {
		t.Fatalf("expected 2 chpwd hooks, got %d", len(cfg.Shell.Hooks.Chpwd))
	}
	if cfg.Shell.Hooks.Chpwd[0] != `zoxide add -- "$PWD"` {
		t.Errorf("Hooks.Chpwd[0] = %q, want %q", cfg.Shell.Hooks.Chpwd[0], `zoxide add -- "$PWD"`)
	}
	if cfg.Shell.Hooks.Chpwd[1] != "echo changed" {
		t.Errorf("Hooks.Chpwd[1] = %q, want %q", cfg.Shell.Hooks.Chpwd[1], "echo changed")
	}
}

func TestLoadConfig_HooksChpwd_Default(t *testing.T) {
	cfg := Default()

	// Default should have no chpwd hooks
	if len(cfg.Shell.Hooks.Chpwd) != 0 {
		t.Errorf("expected 0 default chpwd hooks, got %d", len(cfg.Shell.Hooks.Chpwd))
	}
}

func TestLoadConfig_AgentConversationIdleTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := []byte(`
[agent]
conversation_idle_timeout = "30m"
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil { //nolint:gosec // G306: test file
		t.Fatal(err)
	}

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Agent.ConversationIdleTimeout != "30m" {
		t.Errorf("Agent.ConversationIdleTimeout = %q, want %q", cfg.Agent.ConversationIdleTimeout, "30m")
	}
}
