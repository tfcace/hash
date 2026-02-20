package completion

import (
	"context"
	"strings"
)

// BrewHandler provides completions for brew commands.
type BrewHandler struct {
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)
}

// NewBrewHandler creates a brew completion handler.
func NewBrewHandler() *BrewHandler {
	return &BrewHandler{runCommand: runIsolatedCommand}
}

// Commands returns the commands this handler supports.
func (h *BrewHandler) Commands() []string {
	return []string{"brew"}
}

// Complete returns brew package completions for relevant subcommands.
func (h *BrewHandler) Complete(ctx context.Context, args []string, current string) Result {
	if len(args) == 0 {
		return Result{}
	}

	subCmd := args[0]
	switch subCmd {
	case "uninstall", "upgrade", "info", "reinstall":
		// Continue to complete installed packages
	default:
		return Result{}
	}

	packages := h.listInstalled(ctx)
	return prefixFilterItems(packages, current)
}

func (h *BrewHandler) listInstalled(ctx context.Context) []string {
	queryCtx, cancel := context.WithTimeout(ctx, vcsQueryTimeout)
	defer cancel()

	// List both formulae and casks
	formulae, err := h.runCommand(queryCtx, "brew", "list", "--formula", "-1")
	if err != nil {
		formulae = nil
	}

	casks, err := h.runCommand(queryCtx, "brew", "list", "--cask", "-1")
	if err != nil {
		casks = nil
	}

	seen := make(map[string]bool)
	var packages []string
	for _, list := range [][]string{formulae, casks} {
		for _, pkg := range list {
			pkg = strings.TrimSpace(pkg)
			if pkg != "" && !seen[pkg] {
				seen[pkg] = true
				packages = append(packages, pkg)
			}
		}
	}
	return packages
}
