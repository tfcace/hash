package shell

import (
	"fmt"
	"io"
)

// ActionEntry records a single tool action during an agentic turn.
type ActionEntry struct {
	ToolName string
	Command  string
	Allowed  bool
}

// ActionLog tracks tool actions during an agentic turn and renders them.
type ActionLog struct {
	out     io.Writer
	actions []ActionEntry
}

// NewActionLog creates a new action log that renders to the given writer.
func NewActionLog(out io.Writer) *ActionLog {
	return &ActionLog{out: out}
}

// Add records an action and renders it immediately.
func (al *ActionLog) Add(toolName, command string, allowed bool) {
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
	return al.actions
}

// HasEdits returns true if any write/edit actions were allowed.
func (al *ActionLog) HasEdits() bool {
	for _, a := range al.actions {
		if a.Allowed && isEditTool(a.ToolName) {
			return true
		}
	}
	return false
}

// EditedFiles returns the unique set of files that were written/edited.
func (al *ActionLog) EditedFiles() []string {
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
	return len(al.actions)
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
