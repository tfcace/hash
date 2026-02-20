package completion

import (
	"context"
	"strings"
)

// PipHandler provides completions for pip/pip3 uninstall.
type PipHandler struct {
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)
}

// NewPipHandler creates a pip completion handler.
func NewPipHandler() *PipHandler {
	return &PipHandler{runCommand: runIsolatedCommand}
}

// Commands returns the commands this handler supports.
func (h *PipHandler) Commands() []string {
	return []string{"pip", "pip3"}
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

	lines, err := h.runCommand(queryCtx, "pip3", "freeze")
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
