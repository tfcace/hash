package allowlist

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Manager handles allowed command persistence.
type Manager struct {
	scope      string // "project", "global", "session"
	projectDir string
	globalDir  string

	mu       sync.RWMutex
	commands map[string]bool
}

type fileFormat struct {
	AllowedCommands []string `json:"allowed_commands"`
}

// New creates a new allowlist manager.
// - scope: "project", "global", or "session"
// - projectDir: current working directory (for project scope)
// - globalDir: config directory like ~/.config/hash (for global scope)
func New(scope, projectDir, globalDir string) *Manager {
	m := &Manager{
		scope:      scope,
		projectDir: projectDir,
		globalDir:  globalDir,
		commands:   make(map[string]bool),
	}
	// Auto-load on creation (ignore errors for missing files)
	_ = m.Load()
	return m
}

// SetProjectDir switches the manager to a new project scope and reloads the
// persisted allowlist for that directory. Non-project scopes are unaffected.
func (m *Manager) SetProjectDir(projectDir string) error {
	if m == nil || m.scope != "project" {
		return nil
	}

	newPath := projectAllowlistFilePath(projectDir, m.globalDir)

	m.mu.RLock()
	oldPath := projectAllowlistFilePath(m.projectDir, m.globalDir)
	m.mu.RUnlock()

	if newPath == oldPath {
		return nil
	}

	m.mu.Lock()
	m.projectDir = projectDir
	m.commands = make(map[string]bool)
	m.mu.Unlock()

	return m.Load()
}

// IsAllowed checks if a command is in the allowlist.
func (m *Manager) IsAllowed(command string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.commands[command]
}

// Allow adds a command to the allowlist and persists it.
func (m *Manager) Allow(command string) error {
	m.mu.Lock()
	m.commands[command] = true
	m.mu.Unlock()

	// Session scope doesn't persist
	if m.scope == "session" {
		return nil
	}

	return m.Save()
}

// Load reads the allowlist from disk.
func (m *Manager) Load() error {
	if m.scope == "session" {
		return nil
	}

	path := m.filePath()
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cmd := range f.AllowedCommands {
		m.commands[cmd] = true
	}

	return nil
}

// Save writes the allowlist to disk.
func (m *Manager) Save() error {
	if m.scope == "session" {
		return nil
	}

	path := m.filePath()
	if path == "" {
		return nil
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	m.mu.RLock()
	var cmds []string
	for cmd := range m.commands {
		cmds = append(cmds, cmd)
	}
	m.mu.RUnlock()

	f := fileFormat{AllowedCommands: cmds}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

func (m *Manager) filePath() string {
	switch m.scope {
	case "project":
		return projectAllowlistFilePath(m.projectDir, m.globalDir)
	case "global":
		if m.globalDir == "" {
			return ""
		}
		return filepath.Join(m.globalDir, "allowed_commands.json")
	default:
		return ""
	}
}

func projectAllowlistFilePath(projectDir, globalDir string) string {
	if projectDir == "" || globalDir == "" {
		return ""
	}
	return filepath.Join(globalDir, "project_allowlists", projectScopeKey(projectDir)+".json")
}

func projectScopeKey(projectDir string) string {
	canonical := filepath.Clean(projectDir)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	if abs, err := filepath.Abs(canonical); err == nil {
		canonical = abs
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
