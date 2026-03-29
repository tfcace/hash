package completion

import (
	"context"
	"strings"
)

// PipHandler provides completions for pip/pip3 uninstall.
type PipHandler struct {
	command    string
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)
}

// NewPipHandler creates a pip completion handler.
func NewPipHandler(command string) *PipHandler {
	return &PipHandler{command: command, runCommand: runIsolatedCommand}
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
	queryCtx, cancel := context.WithTimeout(ctx, vcsQueryTimeout)
	defer cancel()

	command := h.command
	if command == "" {
		command = "pip3"
	}

	lines, err := h.runCommand(queryCtx, command, "freeze")
	if err != nil {
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
	return packages
}
