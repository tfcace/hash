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
dialect = 123

[history]
path = "/custom/history.db"

[agent]
command = "my-agent"
`)

	cfg := Load(dir)
	err := cfg.LoadIssue
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
	if cfg.Shell.Dialect != "bash" {
		t.Errorf("Shell.Dialect = %q, want default %q", cfg.Shell.Dialect, "bash")
	}
}

func TestLoad_SyntaxErrorFallsBackWithPosition(t *testing.T) {
	dir := writeTestConfig(t, "[shell\ndialect = \"zsh\"\n")

	cfg := Load(dir)
	err := cfg.LoadIssue
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
dialect = 123

[agent]
default = "mine"

[agent.mine]
transport = "stdio"
command = "my-agent"
`)

	cfg := Load(dir)
	err := cfg.LoadIssue
	if err == nil {
		t.Fatal("expected an error for the invalid [shell] section")
	}
	if got := cfg.EffectiveAgent().Command; got != "my-agent" {
		t.Errorf("EffectiveAgent().Command = %q, want named agent preserved", got)
	}
}

func TestLoadError_WarningIsLoudAndSpecific(t *testing.T) {
	le := &LoadError{
		Kind:        LoadPartial,
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
	dir := writeTestConfig(t, "[shell]\ndialect = \"zsh\"\n")

	cfg := Load(dir)
	err := cfg.LoadIssue
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LoadIssue != nil {
		t.Errorf("LoadIssue = %v, want nil", cfg.LoadIssue)
	}
	if cfg.Shell.Dialect != "zsh" {
		t.Errorf("Shell.Dialect = %q, want zsh", cfg.Shell.Dialect)
	}
}

func TestLoad_BrokenConfigCarriesLoadIssue(t *testing.T) {
	dir := writeTestConfig(t, "[shell]\ndialect = 123\n")

	cfg := Load(dir)
	err := cfg.LoadIssue
	if err == nil {
		t.Fatal("expected error")
	}
	if cfg.LoadIssue == nil {
		t.Fatal("recovered config should carry its LoadIssue for status reporting")
	}
}

// A config.toml that exists but cannot be read (here: it is a directory)
// must still yield a usable config. Returning nil made the login shell
// dereference a nil config and panic before the first prompt.
func TestLoad_ReadErrorFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "config.toml"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Load(dir)
	err := cfg.LoadIssue
	if err == nil {
		t.Fatal("expected an error for an unreadable config.toml")
	}
	if cfg == nil {
		t.Fatal("Load returned a nil config on a read error")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("error should be a *LoadError, got %T: %v", err, err)
	}
	if le.Kind != LoadReadFailed {
		t.Errorf("Kind = %v, want LoadReadFailed", le.Kind)
	}
	if cfg.Agent.Command != "claude-agent-acp" {
		t.Errorf("Agent.Command = %q, want the default", cfg.Agent.Command)
	}
	if w := le.Warning(); !strings.Contains(w, "could not be read") {
		t.Errorf("warning should say the file could not be read, got %q", w)
	}
}

func TestLoad_IssueKinds(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    LoadErrorKind
	}{
		{"syntax", "[shell\n", LoadSyntaxError},
		{"partial", "[shell]\ndialect = 123\n", LoadPartial},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Load(writeTestConfig(t, tt.content))
			err := cfg.LoadIssue
			var le *LoadError
			if !errors.As(err, &le) {
				t.Fatalf("error should be a *LoadError, got %T: %v", err, err)
			}
			if le.Kind != tt.want || cfg.LoadIssue.Kind != tt.want {
				t.Errorf("Kind = %v, want %v", le.Kind, tt.want)
			}
		})
	}
}

// [agent].headers is a field of the flat agent config, not a named agent.
func TestLoad_AgentHeadersTableIsNotANamedAgent(t *testing.T) {
	cfg := Load(writeTestConfig(t, `
[agent]
transport = "http"

[agent.headers]
Authorization = "Bearer x"

[agent.mine]
command = "my-agent"
`))
	err := cfg.LoadIssue
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := cfg.Agents["headers"]; ok {
		t.Error("[agent.headers] was loaded as a named agent")
	}
	if cfg.Agent.Headers["Authorization"] != "Bearer x" {
		t.Errorf("Agent.Headers = %v, want the flat headers", cfg.Agent.Headers)
	}
	if _, ok := cfg.Agents["mine"]; !ok {
		t.Error("named agent [agent.mine] should still load")
	}
}

// keybindings was a setting the editor never read. Configs written while it
// was documented still set it, and must keep loading clean.
func TestLoad_RetiredKeybindingsSettingStillLoadsClean(t *testing.T) {
	dir := writeTestConfig(t, "[shell]\nkeybindings = \"vim\"\n\n[input]\nkeybindings = \"emacs\"\ngutter = true\n")

	cfg := Load(dir)
	err := cfg.LoadIssue
	if err != nil || cfg.LoadIssue != nil {
		t.Fatalf("Load() err = %v, LoadIssue = %v; want a clean load", err, cfg.LoadIssue)
	}
	if !cfg.Input.Gutter {
		t.Error("Input.Gutter = false, want the rest of [input] applied")
	}
}
