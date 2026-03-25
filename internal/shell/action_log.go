package shell

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// ActionEntry records a single tool action during an agentic turn.
type ActionEntry struct {
	ToolName string
	Command  string
	Allowed  bool
}

// fileSnapshot holds a file's original content and permissions.
type fileSnapshot struct {
	content []byte
	mode    os.FileMode
}

// ActionLog tracks tool actions during an agentic turn and renders them.
type ActionLog struct {
	mu        sync.Mutex
	out       io.Writer
	actions   []ActionEntry
	snapshots map[string]*fileSnapshot // file path → original content+mode before edit (nil = newly created)
}

// NewActionLog creates a new action log that renders to the given writer.
func NewActionLog(out io.Writer) *ActionLog {
	return &ActionLog{out: out}
}

// Add records an action and renders it immediately.
func (al *ActionLog) Add(toolName, command string, allowed bool) {
	al.mu.Lock()
	defer al.mu.Unlock()
	entry := ActionEntry{
		ToolName: toolName,
		Command:  command,
		Allowed:  allowed,
	}
	al.actions = append(al.actions, entry)
	al.renderAction(entry)
}

// Summary returns all recorded actions.
func (al *ActionLog) Summary() []ActionEntry {
	al.mu.Lock()
	defer al.mu.Unlock()
	result := make([]ActionEntry, len(al.actions))
	copy(result, al.actions)
	return result
}

// HasEdits returns true if any write/edit actions were allowed.
func (al *ActionLog) HasEdits() bool {
	al.mu.Lock()
	defer al.mu.Unlock()
	for _, a := range al.actions {
		if a.Allowed && isEditTool(a.ToolName) {
			return true
		}
	}
	return false
}

// EditedFiles returns the unique set of files that were written/edited.
func (al *ActionLog) EditedFiles() []string {
	al.mu.Lock()
	defer al.mu.Unlock()
	seen := make(map[string]bool)
	var files []string
	for _, a := range al.actions {
		if a.Allowed && isEditTool(a.ToolName) && !seen[a.Command] {
			seen[a.Command] = true
			files = append(files, a.Command)
		}
	}
	return files
}

// Count returns the total number of actions.
func (al *ActionLog) Count() int {
	al.mu.Lock()
	defer al.mu.Unlock()
	return len(al.actions)
}

// SnapshotFile saves a copy of a file's content and permissions before editing.
func (al *ActionLog) SnapshotFile(path string) error {
	al.mu.Lock()
	defer al.mu.Unlock()
	if al.snapshots == nil {
		al.snapshots = make(map[string]*fileSnapshot)
	}
	// Only snapshot the first time (original content)
	if _, exists := al.snapshots[path]; exists {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet — snapshot as nil (revert = delete)
			al.snapshots[path] = nil
			return nil
		}
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	al.snapshots[path] = &fileSnapshot{content: data, mode: info.Mode()}
	return nil
}

// RevertEdits restores all snapshotted files to their original content and permissions.
// Returns the number of files reverted.
func (al *ActionLog) RevertEdits() int {
	al.mu.Lock()
	defer al.mu.Unlock()
	count := 0
	for path, snap := range al.snapshots {
		if snap == nil {
			os.Remove(path)
			count++
			continue
		}
		if err := os.WriteFile(path, snap.content, snap.mode); err == nil {
			count++
		}
	}
	return count
}

func (al *ActionLog) renderAction(entry ActionEntry) {
	icon := "\x1b[32m\u2713\x1b[0m"
	if !entry.Allowed {
		icon = "\x1b[31m\u2717\x1b[0m"
	}

	// Truncate long commands
	cmd := entry.Command
	if len(cmd) > 72 {
		cmd = cmd[:69] + "..."
	}

	label := toolLabel(entry.ToolName)
	fmt.Fprintf(al.out, "  %s %s %s\n", icon, label, cmd)
}

func isEditTool(toolName string) bool {
	return toolName == "Write" || toolName == "Edit"
}

func toolLabel(toolName string) string {
	switch toolName {
	case "Read", "Glob", "Grep", "Search":
		return "\x1b[90mRead\x1b[0m"
	case "Write", "Edit":
		return "\x1b[33mEdit\x1b[0m"
	case "Bash":
		return "\x1b[90mRun \x1b[0m"
	default:
		return "\x1b[90m" + toolName + "\x1b[0m"
	}
}
