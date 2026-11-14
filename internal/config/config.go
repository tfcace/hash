package config

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

// Config represents the Hash shell configuration.
type Config struct {
	Shell       ShellConfig              `toml:"shell"`
	Input       InputConfig              `toml:"input"`
	Prompt      PromptConfig             `toml:"prompt"`
	Agent       AgentConfig              `toml:"agent"`
	Agents      map[string]AgentEndpoint `toml:"-"`
	History     HistoryConfig            `toml:"history"`
	Completions CompletionsConfig        `toml:"completions"`
	Clipboard   ClipboardConfig          `toml:"clipboard"`
	Prediction  PredictionConfig         `toml:"prediction"`

	// LoadIssue records what went wrong while loading this config, so the
	// shell can surface it (e.g. in `hash status`). Nil when loading was clean.
	LoadIssue *LoadError `toml:"-"`
}

type ShellConfig struct {
	Editor          string             `toml:"editor"`
	Dialect         string             `toml:"dialect"`          // "bash" or "zsh" parser dialect
	InitCommands    []string           `toml:"init_commands"`    // Legacy: run always
	ProfileCommands []string           `toml:"profile"`          // Run on login shells
	RCCommands      []string           `toml:"rc_commands"`      // Run on interactive shells
	DisableBuiltins []string           `toml:"disable_builtins"` // e.g., ["cd"] to use zoxide
	StartupFiles    StartupFilesConfig `toml:"startup_files"`
	Hooks           HooksConfig        `toml:"hooks"`
}

// HooksConfig configures shell hooks.
type HooksConfig struct {
	Chpwd []string `toml:"chpwd"` // Commands to run when working directory changes
}

// StartupFilesConfig specifies which files to source at startup.
type StartupFilesConfig struct {
	Login       []string `toml:"login"`       // Files to source for login shells
	Interactive []string `toml:"interactive"` // Files to source for interactive shells
}

// InputConfig configures the input/editing mode.
type InputConfig struct {
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
	Default              string            `toml:"default"`
	Timeout              string            `toml:"timeout"`
	Transport            string            `toml:"transport"`              // "stdio" or "http"
	Command              string            `toml:"command"`                // For stdio transport
	Args                 []string          `toml:"args"`                   // For stdio transport
	URL                  string            `toml:"url"`                    // For http transport (e.g., "http://localhost:11434/api/generate")
	Model                string            `toml:"model"`                  // For http transport (e.g., "codellama")
	Headers              map[string]string `toml:"headers"`                // For http transport
	AllowedCommandsScope string            `toml:"allowed_commands_scope"` // "project", "global", "session"
	AutoUpdate           string            `toml:"auto_update"`            // "check" (default) or "off": look for a newer claude-agent-acp daily
}

// AgentEndpoint is an individual named agent under [agent.<name>].
type AgentEndpoint struct {
	Transport string            `toml:"transport"`
	Command   string            `toml:"command"`
	Args      []string          `toml:"args"`
	URL       string            `toml:"url"`
	Model     string            `toml:"model"`
	Headers   map[string]string `toml:"headers"`
	Timeout   string            `toml:"timeout"`
}

type HistoryConfig struct {
	Enabled    bool   `toml:"enabled"`
	Path       string `toml:"path"`
	MaxEntries string `toml:"max_entries"`
	MaxAge     string `toml:"max_age"`
}

type CompletionsConfig struct {
	Fuzzy            bool     `toml:"fuzzy"`
	FileIcons        bool     `toml:"file_icons"`
	CobraEnabled     bool     `toml:"cobra_enabled"`
	CobraCommands    []string `toml:"cobra_commands"`     // Cobra tools to ask for __complete beyond the built-in list (kubectl, helm, gh, docker, podman, minikube, kind)
	MaskSensitiveEnv bool     `toml:"mask_sensitive_env"` // Mask values of sensitive env vars (KEY, SECRET, TOKEN, etc.)
	PluginsEnabled   bool     `toml:"plugins_enabled"`    // Declarative completion plugins (built-in + <config>/completions/*.toml)
}

type ClipboardConfig struct {
	MaxOutputSize  string `toml:"max_output_size"`
	BufferSize     int    `toml:"buffer_size"`
	PreserveColors bool   `toml:"preserve_colors"`
}

// PredictionConfig configures command prediction.
type PredictionConfig struct {
	Enabled             bool     `toml:"enabled"`
	AcceptKeys          []string `toml:"accept_keys"`
	ConfidenceThreshold float64  `toml:"confidence_threshold"`
	// RecencyWindowHours is the recency window for scoring command
	// sequences. Its key keeps the name of the path prediction it was
	// first meant for, so existing config files still set it.
	RecencyWindowHours int `toml:"path_recency_boost_hours"`
}

// ParseMaxOutputSize parses the MaxOutputSize string and returns bytes.
func (c *ClipboardConfig) ParseMaxOutputSize() (int64, error) {
	return ParseSize(c.MaxOutputSize)
}

// Default returns a Config with default values.
func Default() *Config {
	return &Config{
		Shell: ShellConfig{
			Editor:  os.Getenv("EDITOR"),
			Dialect: "bash",
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
			Gutter:       true,
			MaxPasteSize: "10MB",
		},
		Prompt: PromptConfig{
			Mode: "starship",
		},
		Agent: AgentConfig{
			Default:              "claude-agent-acp",
			Command:              "claude-agent-acp",
			Timeout:              "120s",
			AllowedCommandsScope: "project",
			AutoUpdate:           "check",
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
			PluginsEnabled:   true,
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
			RecencyWindowHours:  24,
		},
	}
}

// Load reads configuration from the given config directory, falling back to
// defaults for missing values. It always returns a usable config: when the
// file could not be fully loaded it holds defaults plus whatever could be
// salvaged, and cfg.LoadIssue says what went wrong (startup warns with it,
// status reports it).
func Load(configDir string) *Config {
	cfg := Default()

	configPath := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			cfg.LoadIssue = &LoadError{Kind: LoadReadFailed, Path: configPath, Detail: err.Error(), Err: err}
		}
		return cfg
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return recoverConfig(configPath, data, err)
	}

	cfg.loadNamedAgents(decodeAgentTables(data))
	applyEmptyDefaults(cfg)

	return cfg
}

// decodeAgentTables decodes just the [agent] section into a raw map. The
// named [agent.<name>] tables aren't fields of Config, so they need a raw
// decode; targeting only the agent key lets the decoder skip every other
// section instead of building a map of the whole file.
func decodeAgentTables(data []byte) map[string]interface{} {
	var doc struct {
		Agent map[string]interface{} `toml:"agent"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return doc.Agent
}

// applyEmptyDefaults fills defaults for values the file left empty.
func applyEmptyDefaults(cfg *Config) {
	if cfg.Shell.Dialect == "" {
		cfg.Shell.Dialect = "bash"
	}
	if cfg.Prompt.Mode == "" {
		cfg.Prompt.Mode = "starship"
	}
}

// recoverConfig salvages what it can from a config file that failed to
// decode. Sections that decode individually keep the user's values; broken
// ones fall back to defaults. A syntax error means nothing can be salvaged.
// What went wrong is recorded in cfg.LoadIssue.
func recoverConfig(configPath string, data []byte, cause error) *Config {
	detail := decodeErrorDetail(cause)

	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		cfg := Default()
		cfg.LoadIssue = &LoadError{Kind: LoadSyntaxError, Path: configPath, Detail: detail, Err: cause}
		return cfg
	}

	cfg := Default()
	var bad []string
	for name, val := range raw {
		sub, err := toml.Marshal(map[string]any{name: val})
		if err != nil {
			bad = append(bad, name)
			continue
		}
		// Probe into a throwaway config first so a section that fails
		// mid-decode cannot leave half-applied values behind.
		if err := toml.Unmarshal(sub, Default()); err != nil {
			bad = append(bad, name)
			continue
		}
		_ = toml.Unmarshal(sub, cfg)
	}
	sort.Strings(bad)

	// The raw document is already decoded; no need to parse the file again.
	agentRaw, _ := raw["agent"].(map[string]interface{})
	cfg.loadNamedAgents(agentRaw)
	applyEmptyDefaults(cfg)

	cfg.LoadIssue = &LoadError{Kind: LoadPartial, Path: configPath, BadSections: bad, Detail: detail, Err: cause}
	return cfg
}

// EffectiveAgent returns the concrete agent settings selected by [agent].default.
// It supports both the original flat [agent] shape and documented [agent.<name>] tables.
func (c *Config) EffectiveAgent() AgentConfig {
	if c == nil {
		return Default().Agent
	}
	agent := c.Agent
	if agent.Default == "" || len(c.Agents) == 0 {
		return agent
	}

	named, ok := c.Agents[agent.Default]
	if !ok {
		return agent
	}

	if named.Transport != "" {
		agent.Transport = named.Transport
	}
	if named.Command != "" {
		agent.Command = named.Command
	}
	if named.Args != nil {
		agent.Args = named.Args
	}
	if named.URL != "" {
		agent.URL = named.URL
	}
	if named.Model != "" {
		agent.Model = named.Model
	}
	if named.Headers != nil {
		agent.Headers = named.Headers
	}
	if named.Timeout != "" {
		agent.Timeout = named.Timeout
	}

	return agent
}

// loadNamedAgents fills c.Agents from the already-decoded [agent] section.
func (c *Config) loadNamedAgents(agentRaw map[string]interface{}) {
	if len(agentRaw) == 0 {
		return
	}

	agents := make(map[string]AgentEndpoint)
	for name, value := range agentRaw {
		table, ok := value.(map[string]interface{})
		// headers is the flat [agent] config's own table, not a named agent.
		if !ok || name == "headers" {
			continue
		}
		agents[name] = AgentEndpoint{
			Transport: stringValue(table, "transport"),
			Command:   stringValue(table, "command"),
			Args:      stringSliceValue(table, "args"),
			URL:       stringValue(table, "url"),
			Model:     stringValue(table, "model"),
			Headers:   stringMapValue(table, "headers"),
			Timeout:   stringValue(table, "timeout"),
		}
	}

	if len(agents) > 0 {
		c.Agents = agents
	}
}

func stringValue(values map[string]interface{}, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}

func stringSliceValue(values map[string]interface{}, key string) []string {
	raw, ok := values[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if str, ok := value.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func stringMapValue(values map[string]interface{}, key string) map[string]string {
	raw, ok := values[key].(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, value := range raw {
		if str, ok := value.(string); ok {
			out[k] = str
		}
	}
	return out
}
