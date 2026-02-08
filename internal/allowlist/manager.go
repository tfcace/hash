package allowlist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

	// For project scope, refuse to load if the file is tracked by version
	// control. A malicious repository could ship a pre-populated allowlist
	// to auto-approve dangerous commands without user interaction.
	//
	// If tracking cannot be verified (e.g., git unavailable in PATH), fail
	// closed and refuse to load.
	if m.scope == "project" {
		tracked, trackErr := isTrackedByGit(path)
		if trackErr != nil {
			return fmt.Errorf("allowlist: refusing to load %s: unable to verify git tracking: %w", path, trackErr)
		}
		if tracked {
			return fmt.Errorf("allowlist: refusing to load %s: file is tracked by git (potential supply-chain risk)", path)
		}
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

// isTrackedByGit returns whether the path is tracked by git.
// Returns (false, nil) if the directory is not in a git repository.
func isTrackedByGit(path string) (bool, error) {
	repoRoot, inRepo := findGitRoot(filepath.Dir(path))
	if !inRepo {
		return false, nil
	}

	relPath, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return false, fmt.Errorf("relative path to git root: %w", err)
	}

	cmd := exec.Command("git", "ls-files", "--error-unmatch", "--", filepath.ToSlash(relPath))
	cmd.Dir = repoRoot
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			// Valid git invocation, but file is not tracked.
			return false, nil
		}
		return false, fmt.Errorf("git ls-files: %w", err)
	}

	return true, nil
}

// findGitRoot walks parent directories and returns the first directory that
// contains a .git entry (directory or file).
func findGitRoot(startDir string) (string, bool) {
	dir := startDir
	for {
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
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
		if m.projectDir == "" {
			return ""
		}
		return filepath.Join(m.projectDir, ".hash", "allowed_commands.json")
	case "global":
		if m.globalDir == "" {
			return ""
		}
		return filepath.Join(m.globalDir, "allowed_commands.json")
	default:
		return ""
	}
}
