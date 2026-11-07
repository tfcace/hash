package completion

import (
	"context"
	"strings"
	"time"
)

// processListTimeout bounds one ps query.
const processListTimeout = 2 * time.Second

// KillHandler provides completions for kill and killall commands.
type KillHandler struct {
	command       string
	listProcesses func(ctx context.Context) ([]processInfo, error)
	cacheTTL      time.Duration
	processes     flightCache[[]processInfo]
}

type processInfo struct {
	PID  string
	Name string
}

// NewKillHandler creates a kill/killall completion handler.
func NewKillHandler(command string) *KillHandler {
	return &KillHandler{
		command:       command,
		listProcesses: defaultListProcesses,
		cacheTTL:      time.Second,
	}
}

func (h *KillHandler) setReady(n *readyNotifier) { h.processes.ready = n }

// Commands returns the commands this handler supports.
func (h *KillHandler) Commands() []string {
	if h.command == "" {
		return []string{"kill"}
	}
	return []string{h.command}
}

// Complete returns process completions.
func (h *KillHandler) Complete(ctx context.Context, args []string, current string) Result {
	if strings.HasPrefix(current, "-") {
		return Result{}
	}

	policy := flightPolicy{ttl: h.cacheTTL, failureTTL: h.cacheTTL, timeout: processListTimeout, wait: sourceWait}
	processes, status := h.processes.get(ctx, "processes", policy, func(ctx context.Context) ([]processInfo, error) {
		list := h.listProcesses
		if list == nil {
			list = defaultListProcesses
		}
		return list(ctx)
	})
	if status == lookupPending || status == lookupFailed {
		return sourceResult(nil, status, current)
	}

	var result Result
	seen := make(map[string]bool)
	for _, p := range processes {
		item := Item{Value: p.PID, Display: p.PID, Description: p.Name}
		if h.command == "killall" {
			item = Item{Value: p.Name, Display: p.Name}
		}
		if seen[item.Value] || !strings.HasPrefix(item.Value, current) {
			continue
		}
		seen[item.Value] = true
		if !result.add(item) {
			break
		}
	}
	return result
}

func defaultListProcesses(ctx context.Context) ([]processInfo, error) {
	lines, err := runIsolatedCommand(ctx, "ps", "-eo", "pid,comm")
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
