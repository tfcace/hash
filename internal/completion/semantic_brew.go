package completion

import (
	"context"
	"strings"
	"sync"
	"time"
)

// BrewHandler provides completions for brew commands.
type BrewHandler struct {
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)
	cache      stringListCache
	cacheTTL   time.Duration
	now        func() time.Time
}

// NewBrewHandler creates a brew completion handler.
func NewBrewHandler() *BrewHandler {
	return &BrewHandler{
		runCommand: runIsolatedCommand,
		cacheTTL:   10 * time.Second,
		now:        time.Now,
	}
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
	if h.cacheTTL > 0 {
		if packages, ok := h.cache.get("installed", h.timeNow()); ok {
			return packages
		}
	}

	queryCtx, cancel := context.WithTimeout(ctx, vcsQueryTimeout)
	defer cancel()

	// List both formulae and casks
	var formulae, casks []string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		lines, err := h.runCommand(queryCtx, "brew", "list", "--formula", "-1")
		if err == nil {
			formulae = lines
		}
	}()
	go func() {
		defer wg.Done()
		lines, err := h.runCommand(queryCtx, "brew", "list", "--cask", "-1")
		if err == nil {
			casks = lines
		}
	}()
	wg.Wait()

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
	if h.cacheTTL > 0 {
		h.cache.set("installed", packages, h.timeNow().Add(h.cacheTTL))
	}
	return packages
}

func (h *BrewHandler) timeNow() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}
