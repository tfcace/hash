package config

import (
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Config represents the Hash shell configuration.
type Config struct {
	Shell       ShellConfig       `toml:"shell"`
	Input       InputConfig       `toml:"input"`
	Prompt      PromptConfig      `toml:"prompt"`
	Agent       AgentConfig       `toml:"agent"`
	History     HistoryConfig     `toml:"history"`
	Completions CompletionsConfig `toml:"completions"`
}

type ShellConfig struct {
	Editor          string             `toml:"editor"`
	Keybindings     string             `toml:"keybindings"`
	InitCommands    []string           `toml:"init_commands"`    // Legacy: run always
	ProfileCommands []string           `toml:"profile"`          // Run on login shells
	RCCommands      []string           `toml:"rc_commands"`      // Run on interactive shells
	DisableBuiltins []string           `toml:"disable_builtins"` // e.g., ["cd"] to use zoxide
	StartupFiles    StartupFilesConfig `toml:"startup_files"`
}

// StartupFilesConfig specifies which files to source at startup.
type StartupFilesConfig struct {
	Login       []string `toml:"login"`       // Files to source for login shells
	Interactive []string `toml:"interactive"` // Files to source for interactive shells
}

// InputConfig configures the input/editing mode.
type InputConfig struct {
	Mode        string `toml:"mode"`        // "editor" or "readline"
	Keybindings string `toml:"keybindings"` // "helix", "emacs", "vim"
	Gutter      bool   `toml:"gutter"`      // Show visual indicator for multiline
}

type PromptConfig struct {
	Mode         string `toml:"mode"`
	StarshipPath string `toml:"starship_path"`
	Alignment    string `toml:"alignment"`      // "left" or "right"
	DevMode      bool   `toml:"dev_mode"`       // Show dev mode indicator
	DevModeLabel string `toml:"dev_mode_label"` // Custom label (default: "DEV")
}

type AgentConfig struct {
	Default   string   `toml:"default"`
	Timeout   string   `toml:"timeout"`
	Transport string   `toml:"transport"` // "stdio" or "http"
	Command   string   `toml:"command"`   // For stdio transport
	Args      []string `toml:"args"`      // For stdio transport
	URL       string   `toml:"url"`       // For http transport (e.g., "http://localhost:11434/api/generate")
	Model     string   `toml:"model"`     // For http transport (e.g., "codellama")
}

type HistoryConfig struct {
	Enabled    bool   `toml:"enabled"`
	Path       string `toml:"path"`
	MaxEntries string `toml:"max_entries"`
	MaxAge     string `toml:"max_age"`
}

type CompletionsConfig struct {
	Fuzzy        bool `toml:"fuzzy"`
	FileIcons    bool `toml:"file_icons"`
	CobraEnabled bool `toml:"cobra_enabled"`
}

// Default returns a Config with default values.
func Default() *Config {
	return &Config{
		Shell: ShellConfig{
			Editor:      os.Getenv("EDITOR"),
			Keybindings: "emacs",
			StartupFiles: StartupFilesConfig{
				Login: []string{
					"/etc/profile",
					"~/.profile",
					"~/.hash_profile",
				},
				Interactive: []string{
					"~/.hashrc",
				},
			},
		},
		Input: InputConfig{
			Mode:        "editor",
			Keybindings: "helix",
			Gutter:      false,
		},
		Prompt: PromptConfig{
			Mode: "starship",
		},
		Agent: AgentConfig{
			Default: "claude-code-acp",
			Command: "claude-code-acp",
			Timeout: "120s",
		},
		History: HistoryConfig{
			Enabled:    true,
			MaxEntries: "unlimited",
			MaxAge:     "forever",
		},
		Completions: CompletionsConfig{
			Fuzzy:        true,
			FileIcons:    true,
			CobraEnabled: true,
		},
	}
}

// Load reads configuration from the given config directory.
// Falls back to defaults for missing values.
func Load(configDir string) (*Config, error) {
	cfg := Default()

	configPath := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Apply defaults for empty values
	if cfg.Shell.Keybindings == "" {
		cfg.Shell.Keybindings = "emacs"
	}
	if cfg.Input.Mode == "" {
		cfg.Input.Mode = "editor"
	}
	if cfg.Input.Keybindings == "" {
		cfg.Input.Keybindings = "helix"
	}
	if cfg.Prompt.Mode == "" {
		cfg.Prompt.Mode = "starship"
	}

	return cfg, nil
}
