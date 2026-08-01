package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchConfigTOML is a representative user config: every section present,
// comments, and two named agents under [agent.<name>].
const benchConfigTOML = `# hash configuration
[shell]
editor = "nvim"
keybindings = "emacs"
dialect = "bash"
disable_builtins = ["cd"]
rc_commands = ["source ~/.aliases"]

[shell.startup_files]
login = ["/etc/profile", "~/.profile"]
interactive = ["~/.hashrc"]

[input]
keybindings = "helix"
gutter = true
max_paste_size = "10MB"

[prompt]
mode = "starship"
alignment = "left"

[agent]
default = "claude"
timeout = "120s"
allowed_commands_scope = "project"

[agent.claude]
transport = "stdio"
command = "claude-agent-acp"
args = ["--verbose"]

[agent.ollama]
transport = "http"
url = "http://localhost:11434/api/generate"
model = "codellama"

[history]
enabled = true
max_entries = "unlimited"
max_age = "forever"

[completions]
fuzzy = true
file_icons = true
cobra_enabled = true

[clipboard]
max_output_size = "1MB"
buffer_size = 100

[prediction]
enabled = true
accept_keys = ["right", "tab"]
confidence_threshold = 0.6
`

// brokenBenchConfigTOML is benchConfigTOML with one value of the wrong type
// (string into float), which sends Load down the section-by-section recovery
// path while every other section stays salvageable.
func brokenBenchConfigTOML() string {
	return strings.Replace(benchConfigTOML,
		`confidence_threshold = 0.6`,
		`confidence_threshold = "high"`, 1)
}

func benchConfigDir(b *testing.B, content string) string {
	b.Helper()
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
	return dir
}

// BenchmarkLoad measures a clean startup config load.
func BenchmarkLoad(b *testing.B) {
	dir := benchConfigDir(b, benchConfigTOML)
	b.ReportAllocs()
	for b.Loop() {
		cfg, err := Load(dir)
		if err != nil {
			b.Fatal(err)
		}
		if len(cfg.Agents) != 2 {
			b.Fatalf("expected 2 named agents, got %d", len(cfg.Agents))
		}
	}
}

// BenchmarkLoadRecovery measures loading a config with a broken section,
// which decodes every salvageable section individually.
func BenchmarkLoadRecovery(b *testing.B) {
	dir := benchConfigDir(b, brokenBenchConfigTOML())
	b.ReportAllocs()
	for b.Loop() {
		cfg, err := Load(dir)
		if err == nil {
			b.Fatal("expected a load error for the broken section")
		}
		if cfg == nil || cfg.Shell.Editor != "nvim" {
			b.Fatal("recovery did not salvage the shell section")
		}
	}
}
