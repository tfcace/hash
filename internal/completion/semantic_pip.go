package completion

import (
	"context"
	"os"
	"strings"
	"time"
)

// pipFreezeTimeout bounds one `pip freeze`, which takes hundreds of
// milliseconds: it runs detached and the menu fills in when it lands.
const pipFreezeTimeout = 5 * time.Second

// PipHandler provides completions for pip/pip3 uninstall.
type PipHandler struct {
	command    string
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)
	cacheTTL   time.Duration
	installed  flightCache[[]string]
}

// NewPipHandler creates a pip completion handler.
func NewPipHandler(command string) *PipHandler {
	return &PipHandler{
		command:    command,
		runCommand: runIsolatedCommand,
		cacheTTL:   10 * time.Second,
	}
}

func (h *PipHandler) setReady(n *readyNotifier) { h.installed.ready = n }

// Commands returns the commands this handler supports.
func (h *PipHandler) Commands() []string {
	if h.command == "" {
		return []string{"pip3"}
	}
	return []string{h.command}
}

// Complete returns installed package completions for pip uninstall.
func (h *PipHandler) Complete(ctx context.Context, args []string, current string) Result {
	if len(args) == 0 {
		return Result{}
	}
	if args[0] != "uninstall" {
		return Result{}
	}

	command := h.command
	if command == "" {
		command = "pip3"
	}
	// A missing pip fails the same way every time: remember it as long as
	// an answer would be kept.
	policy := flightPolicy{ttl: h.cacheTTL, failureTTL: h.cacheTTL, timeout: pipFreezeTimeout, wait: sourceWait}
	// Keyed by PATH too: activating a venv changes which pip runs.
	key := command + "\x00" + os.Getenv("PATH")
	packages, status := h.installed.get(ctx, key, policy, func(ctx context.Context) ([]string, error) {
		run := h.runCommand
		if run == nil {
			run = runIsolatedCommand
		}
		lines, err := run(ctx, command, "freeze")
		if err != nil {
			return nil, err
		}
		return parsePipFreeze(lines), nil
	})
	return sourceResult(packages, status, current)
}

// parsePipFreeze extracts package names from `pip freeze` lines
// ("package==version").
func parsePipFreeze(lines []string) []string {
	var packages []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "=="); idx > 0 {
			packages = append(packages, line[:idx])
		} else {
			packages = append(packages, line)
		}
	}
	return packages
}
