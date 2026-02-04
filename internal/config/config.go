package config

import (
	"fmt"
	"io"
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
	Clipboard   ClipboardConfig   `toml:"clipboard"`
	Prediction  PredictionConfig  `toml:"prediction"`
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
	Keybindings  string `toml:"keybindings"`    // "helix", "emacs", "vim"
	Gutter       bool   `toml:"gutter"`         // Show visual indicator for multiline
	MaxPasteSize string `toml:"max_paste_size"` // Maximum paste size (e.g., "10MB")
}

// ParseMaxPasteSize parses the MaxPasteSize string and returns bytes.
// Returns default of 10MB if not set or invalid.
func (c *InputConfig) ParseMaxPasteSize() uint {
	if c.MaxPasteSize == "" {
		return 10 * 1024 * 1024 // 10MB default
	}
	size, err := ParseSize(c.MaxPasteSize)
	if err != nil || size < 0 {
		return 10 * 1024 * 1024 // 10MB default on error
	}
	return uint(size)
}

type PromptConfig struct {
	Mode         string `toml:"mode"`
	StarshipPath string `toml:"starship_path"`
	Alignment    string `toml:"alignment"` // "left" or "right"
}

type AgentConfig struct {
	Default              string   `toml:"default"`
	Timeout              string   `toml:"timeout"`
	Transport            string   `toml:"transport"`              // "stdio" or "http"
	Command              string   `toml:"command"`                // For stdio transport
	Args                 []string `toml:"args"`                   // For stdio transport
	URL                  string   `toml:"url"`                    // For http transport (e.g., "http://localhost:11434/api/generate")
	Model                string   `toml:"model"`                  // For http transport (e.g., "codellama")
	AllowedCommandsScope string   `toml:"allowed_commands_scope"` // "project", "global", "session"
}

type HistoryConfig struct {
	Enabled    bool   `toml:"enabled"`
	Path       string `toml:"path"`
	MaxEntries string `toml:"max_entries"`
	MaxAge     string `toml:"max_age"`
}

type CompletionsConfig struct {
	Fuzzy            bool `toml:"fuzzy"`
	FileIcons        bool `toml:"file_icons"`
	CobraEnabled     bool `toml:"cobra_enabled"`
	MaskSensitiveEnv bool `toml:"mask_sensitive_env"` // Mask values of sensitive env vars (KEY, SECRET, TOKEN, etc.)
}

type ClipboardConfig struct {
	MaxOutputSize  string `toml:"max_output_size"`
	BufferSize     int    `toml:"buffer_size"`
	PreserveColors bool   `toml:"preserve_colors"`
}

// PredictionConfig configures command and path prediction.
type PredictionConfig struct {
	Enabled             bool     `toml:"enabled"`
	AcceptKeys          []string `toml:"accept_keys"`
	ConfidenceThreshold float64  `toml:"confidence_threshold"`
	PathMinCount        int      `toml:"path_min_count"`
	PathRecencyHours    int      `toml:"path_recency_boost_hours"`
}

// ParseMaxOutputSize parses the MaxOutputSize string and returns bytes.
func (c *ClipboardConfig) ParseMaxOutputSize() (int64, error) {
	return ParseSize(c.MaxOutputSize)
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
			Keybindings:  "helix",
			Gutter:       true,
			MaxPasteSize: "10MB",
		},
		Prompt: PromptConfig{
			Mode: "starship",
		},
		Agent: AgentConfig{
			Default:              "claude-code-acp",
			Command:              "claude-code-acp",
			Timeout:              "120s",
			AllowedCommandsScope: "project",
		},
		History: HistoryConfig{
			Enabled:    true,
			MaxEntries: "unlimited",
			MaxAge:     "forever",
		},
		Completions: CompletionsConfig{
			Fuzzy:            true,
			FileIcons:        true,
			CobraEnabled:     true,
			MaskSensitiveEnv: true,
		},
		Clipboard: ClipboardConfig{
			MaxOutputSize:  "1MB",
			BufferSize:     100,
			PreserveColors: false,
		},
		Prediction: PredictionConfig{
			Enabled:             true,
			AcceptKeys:          []string{"right", "tab"},
			ConfidenceThreshold: 0.6,
			PathMinCount:        2,
			PathRecencyHours:    24,
		},
	}
}

// Load reads configuration from the given config directory.
// Falls back to defaults for missing values.
// On parse errors, returns defaults with error (shell should warn but continue).
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
		// Return defaults but include error so caller can warn user
		return Default(), fmt.Errorf("config parse error (using defaults): %w", err)
	}

	// Apply defaults for empty values
	if cfg.Shell.Keybindings == "" {
		cfg.Shell.Keybindings = "emacs"
	}
	if cfg.Input.Keybindings == "" {
		cfg.Input.Keybindings = "helix"
	}
	if cfg.Prompt.Mode == "" {
		cfg.Prompt.Mode = "starship"
	}

	return cfg, nil
}

// LoadWithWarnings is like Load but writes warnings to the given writer.
// This is the preferred way to load config as it handles errors gracefully.
func LoadWithWarnings(configDir string, warn io.Writer) *Config {
	cfg, err := Load(configDir)
	if err != nil {
		fmt.Fprintf(warn, "Warning: %v\n", err)
	}
	return cfg
}
