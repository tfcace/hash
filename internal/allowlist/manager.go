package allowlist

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Scope is where approvals persist.
type Scope string

const (
	ScopeSession Scope = "session" // in memory only, gone when the shell exits
	ScopeProject Scope = "project" // one file per project directory
	ScopeGlobal  Scope = "global"  // one file for all projects
)

// ParseScope parses the allowed_commands_scope setting. Empty means the
// default, project. An unknown value is an error; the returned scope is then
// session, which never writes approvals anywhere unexpected.
func ParseScope(s string) (Scope, error) {
	switch scope := Scope(s); scope {
	case ScopeSession, ScopeProject, ScopeGlobal:
		return scope, nil
	case "":
		return ScopeProject, nil
	default:
		return ScopeSession, fmt.Errorf("unknown allowed_commands_scope %q (use project, global, or session)", s)
	}
}

// Manager handles allowed command persistence. For a file scope the file is
// the source of truth: it is read on every check, so an approval the user
// deletes from it is revoked in every shell.
type Manager struct {
	scope     Scope
	globalDir string

	mu         sync.Mutex
	projectDir string
	// session holds the approvals of session scope, which are never saved.
	session map[string]bool
	// unsaved holds, for a file scope, the approvals whose save failed (or
	// had nowhere to go). They are allowed in this shell and every Allow
	// tries to save them again.
	unsaved map[string]bool
}

type fileFormat struct {
	AllowedCommands []string `json:"allowed_commands"`
}

// New creates a new allowlist manager.
// - scope: one of the Scope constants, as ParseScope returns them
// - projectDir: current working directory (for project scope)
// - globalDir: config directory like ~/.config/hash (both file scopes)
func New(scope Scope, projectDir, globalDir string) *Manager {
	return &Manager{
		scope:      scope,
		projectDir: projectDir,
		globalDir:  globalDir,
		session:    make(map[string]bool),
		unsaved:    make(map[string]bool),
	}
}

// SetProjectDir switches the manager to a new project's allowlist. Unsaved
// approvals belonged to the old project and are dropped. Non-project scopes
// are unaffected.
func (m *Manager) SetProjectDir(projectDir string) error {
	if m == nil || m.scope != ScopeProject {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if projectDir != m.projectDir {
		m.projectDir = projectDir
		m.unsaved = make(map[string]bool)
	}
	return nil
}

// IsAllowed checks if a command is in the allowlist. An unreadable file
// allows nothing beyond this shell's unsaved approvals.
func (m *Manager) IsAllowed(command string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session[command] || m.unsaved[command] {
		return true
	}
	path, _ := m.filePath()
	if path == "" {
		return false
	}
	cmds, _ := readFile(path)
	return slices.Contains(cmds, command)
}

// Allow adds a command to the allowlist and persists it. When it cannot be
// persisted (see filePath), it is still allowed for this session and the
// reason is returned.
func (m *Manager) Allow(command string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scope == ScopeSession {
		m.session[command] = true
		return nil
	}
	m.unsaved[command] = true
	return m.saveLocked()
}

// errCorrupt marks an allowlist file that is not valid JSON.
var errCorrupt = errors.New("corrupt allowlist")

// saveLocked adds the unsaved approvals to the file as it is now on disk, so
// approvals other shells saved are kept and ones the user deleted stay gone.
func (m *Manager) saveLocked() error {
	path, err := m.filePath()
	if path == "" {
		return err
	}
	onDisk, err := readFile(path)
	switch {
	case errors.Is(err, errCorrupt):
		// Keep the unreadable file for the user instead of overwriting it.
		if renameErr := os.Rename(path, path+".corrupt"); renameErr != nil {
			return renameErr
		}
	case err != nil:
		return err
	}

	cmds := onDisk
	for cmd := range m.unsaved {
		if !slices.Contains(cmds, cmd) {
			cmds = append(cmds, cmd)
		}
	}
	data, err := json.MarshalIndent(fileFormat{AllowedCommands: cmds}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	clear(m.unsaved)
	return nil
}

// readFile returns the commands in an allowlist file. A missing file has
// none.
func readFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%w %s: %w", errCorrupt, path, err)
	}
	return f.AllowedCommands, nil
}

// writeAtomic replaces path with data so a reader never sees a partial file.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".allowlist-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Why a file scope has no file: approvals then last for the session only.
var (
	errNoConfigDir  = errors.New("allowlist: no config directory to save approvals in")
	errNoProjectDir = errors.New("allowlist: no project directory to save approvals for")
)

// filePath returns the file the scope persists to. Session scope has none by
// design (path "", nil error); a file scope missing a directory returns ""
// and the reason. The caller holds m.mu (projectDir changes with cd).
func (m *Manager) filePath() (string, error) {
	switch {
	case m.scope == ScopeSession:
		return "", nil
	case m.globalDir == "":
		return "", errNoConfigDir
	case m.scope == ScopeGlobal:
		return filepath.Join(m.globalDir, "allowed_commands.json"), nil
	case m.projectDir == "":
		return "", errNoProjectDir
	default: // ScopeProject
		return filepath.Join(m.globalDir, "project_allowlists", projectScopeKey(m.projectDir)+".json"), nil
	}
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
