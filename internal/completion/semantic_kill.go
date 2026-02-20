package completion

import (
	"context"
	"strings"
)

// KillHandler provides completions for kill and killall commands.
type KillHandler struct {
	listProcesses func(ctx context.Context) ([]processInfo, error)
}

type processInfo struct {
	PID  string
	Name string
}

// NewKillHandler creates a kill/killall completion handler.
func NewKillHandler() *KillHandler {
	return &KillHandler{listProcesses: defaultListProcesses}
}

// Commands returns the commands this handler supports.
func (h *KillHandler) Commands() []string {
	return []string{"kill", "killall"}
}

// Complete returns process completions.
func (h *KillHandler) Complete(ctx context.Context, args []string, current string) Result {
	if strings.HasPrefix(current, "-") {
		return Result{}
	}

	processes, err := h.listProcesses(ctx)
	if err != nil {
		return Result{}
	}

	// Determine which command we're completing for
	isKillall := false
	for _, arg := range args {
		// This is a heuristic; the handler doesn't directly receive the command name.
		// The semantic completer maps by command name, so we check context differently.
		_ = arg
	}
	// Since we register for both "kill" and "killall", we need to determine
	// which command. We can tell by checking whether any of the registered
	// names match. We use a simple approach: if the first process info has
	// a non-numeric name and matches current, we're in killall mode.
	// Actually, the handler is shared. We provide PIDs with descriptions for kill,
	// and names for killall. Since we can't distinguish here, we provide both.
	// The filter will handle it.

	// For kill: show PIDs with process names as descriptions
	// For killall: show process names
	// Since we can't tell which command from args alone, provide both
	_ = isKillall

	var items []Item
	seen := make(map[string]bool)
	for _, p := range processes {
		// Add PID completion (for kill)
		if !seen[p.PID] && strings.HasPrefix(p.PID, current) {
			seen[p.PID] = true
			items = append(items, Item{
				Value:       p.PID,
				Display:     p.PID,
				Description: p.Name,
			})
		}
		// Add process name (for killall)
		if !seen[p.Name] && strings.HasPrefix(p.Name, current) {
			seen[p.Name] = true
			items = append(items, Item{
				Value:   p.Name,
				Display: p.Name,
			})
		}
	}
	return Result{Items: items}
}

func defaultListProcesses(ctx context.Context) ([]processInfo, error) {
	queryCtx, cancel := context.WithTimeout(ctx, vcsQueryTimeout)
	defer cancel()

	lines, err := runIsolatedCommand(queryCtx, "ps", "-eo", "pid,comm")
	if err != nil {
		return nil, err
	}

	var processes []processInfo
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid := fields[0]
		// Skip header
		if pid == "PID" {
			continue
		}
		// Extract just the binary name from the path
		name := fields[1]
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		// Strip leading "-" from login shells
		name = strings.TrimPrefix(name, "-")
		if name == "" {
			continue
		}
		processes = append(processes, processInfo{PID: pid, Name: name})
	}
	return processes, nil
}
