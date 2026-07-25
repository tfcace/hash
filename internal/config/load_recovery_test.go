package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return dir
}

func TestLoad_TypeErrorKeepsOtherSections(t *testing.T) {
	dir := writeTestConfig(t, `
[shell]
keybindings = 123

[history]
path = "/custom/history.db"

[agent]
command = "my-agent"
`)

	cfg, err := Load(dir)
	if err == nil {
		t.Fatal("expected an error for the invalid [shell] section")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("error should be a *LoadError, got %T: %v", err, err)
	}
	if len(le.BadSections) != 1 || le.BadSections[0] != "shell" {
		t.Errorf("BadSections = %v, want [shell]", le.BadSections)
	}

	// Valid sections must keep the user's values.
	if cfg.History.Path != "/custom/history.db" {
		t.Errorf("History.Path = %q, want the user's value preserved", cfg.History.Path)
	}
	if cfg.Agent.Command != "my-agent" {
		t.Errorf("Agent.Command = %q, want the user's value preserved", cfg.Agent.Command)
	}
	// The broken section falls back to defaults.
	if cfg.Shell.Keybindings != "emacs" {
		t.Errorf("Shell.Keybindings = %q, want default %q", cfg.Shell.Keybindings, "emacs")
	}
}

func TestLoad_SyntaxErrorFallsBackWithPosition(t *testing.T) {
	dir := writeTestConfig(t, "[shell\nkeybindings = \"vim\"\n")

	cfg, err := Load(dir)
	if err == nil {
		t.Fatal("expected an error for broken TOML syntax")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("error should be a *LoadError, got %T: %v", err, err)
	}
	if len(le.BadSections) != 0 {
		t.Errorf("a syntax error affects the whole file, BadSections = %v", le.BadSections)
	}
	if !strings.Contains(le.Detail, "line 1") {
		t.Errorf("Detail should point at the offending line, got %q", le.Detail)
	}
	// Whole file unusable: defaults apply.
	if cfg == nil || cfg.Agent.Command != "claude-agent-acp" {
		t.Error("expected default config on syntax error")
	}
}

func TestLoad_RecoveredConfigStillLoadsNamedAgents(t *testing.T) {
	dir := writeTestConfig(t, `
[shell]
keybindings = 123

[agent]
default = "mine"

[agent.mine]
transport = "stdio"
command = "my-agent"
`)

	cfg, err := Load(dir)
	if err == nil {
		t.Fatal("expected an error for the invalid [shell] section")
	}
	if got := cfg.EffectiveAgent().Command; got != "my-agent" {
		t.Errorf("EffectiveAgent().Command = %q, want named agent preserved", got)
	}
}

func TestLoadError_WarningIsLoudAndSpecific(t *testing.T) {
	le := &LoadError{
		Path:        "/home/u/.config/hash/config.toml",
		BadSections: []string{"shell"},
		Detail:      "line 3, column 15: cannot decode",
	}

	w := le.Warning()
	if !strings.Contains(w, "/home/u/.config/hash/config.toml") {
		t.Error("warning should name the config file")
	}
	if !strings.Contains(w, "shell") {
		t.Error("warning should name the reverted section")
	}
	if !strings.Contains(w, "line 3") {
		t.Error("warning should include the error position")
	}
	if !strings.Contains(w, "kept") {
		t.Error("warning should say other sections were kept")
	}
}

func TestLoad_ValidConfigHasNoLoadIssue(t *testing.T) {
	dir := writeTestConfig(t, "[shell]\nkeybindings = \"vim\"\n")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LoadIssue != nil {
		t.Errorf("LoadIssue = %v, want nil", cfg.LoadIssue)
	}
	if cfg.Shell.Keybindings != "vim" {
		t.Errorf("Shell.Keybindings = %q, want vim", cfg.Shell.Keybindings)
	}
}

func TestLoad_BrokenConfigCarriesLoadIssue(t *testing.T) {
	dir := writeTestConfig(t, "[shell]\nkeybindings = 123\n")

	cfg, err := Load(dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if cfg.LoadIssue == nil {
		t.Fatal("recovered config should carry its LoadIssue for status reporting")
	}
}
