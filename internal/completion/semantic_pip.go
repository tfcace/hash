package completion

import (
	"context"
	"strings"
	"time"
)

// PipHandler provides completions for pip/pip3 uninstall.
type PipHandler struct {
	command    string
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)
	cache      stringListCache
	cacheTTL   time.Duration
	now        func() time.Time
}

// NewPipHandler creates a pip completion handler.
func NewPipHandler(command string) *PipHandler {
	return &PipHandler{
		command:    command,
		runCommand: runIsolatedCommand,
		cacheTTL:   10 * time.Second,
		now:        time.Now,
	}
}

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

	packages := h.listInstalled(ctx)
	return prefixFilterItems(packages, current)
}

func (h *PipHandler) listInstalled(ctx context.Context) []string {
	command := h.command
	if command == "" {
		command = "pip3"
	}
	if h.cacheTTL > 0 {
		if packages, ok := h.cache.get(command, h.timeNow()); ok {
			return packages
		}
	}

	queryCtx, cancel := context.WithTimeout(ctx, vcsQueryTimeout)
	defer cancel()

	lines, err := runCommandUntilContext(queryCtx, h.runCommand, command, "freeze")
	if err != nil {
		if queryCtx.Err() != nil {
			return nil
		}
		if h.cacheTTL > 0 {
			h.cache.set(command, nil, h.timeNow().Add(h.cacheTTL))
		}
		return nil
	}

	var packages []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: package==version
		if idx := strings.Index(line, "=="); idx > 0 {
			packages = append(packages, line[:idx])
		} else {
			packages = append(packages, line)
		}
	}
	if h.cacheTTL > 0 {
		h.cache.set(command, packages, h.timeNow().Add(h.cacheTTL))
	}
	return packages
}

func (h *PipHandler) timeNow() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}
