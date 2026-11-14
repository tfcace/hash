package completion

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// brewListTimeout bounds one `brew list`, which routinely takes longer than
// a Tab's budget: it runs detached and the menu fills in when it lands.
const brewListTimeout = 5 * time.Second

// BrewHandler provides completions for brew commands.
type BrewHandler struct {
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)
	cacheTTL   time.Duration
	installed  flightCache[[]string]
}

// NewBrewHandler creates a brew completion handler.
func NewBrewHandler() *BrewHandler {
	return &BrewHandler{
		runCommand: runIsolatedCommand,
		cacheTTL:   10 * time.Second,
	}
}

func (h *BrewHandler) setReady(n *readyNotifier) { h.installed.ready = n }

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

	policy := flightPolicy{ttl: h.cacheTTL, failureTTL: h.cacheTTL, timeout: brewListTimeout, wait: sourceWait}
	packages, status := h.installed.get(ctx, "installed", policy, h.collectInstalled)
	return sourceResult(packages, status, current)
}

// collectInstalled lists installed formulae and casks in parallel. One
// failing (casks are unsupported on Linux) still yields the other; it fails
// only when both do.
func (h *BrewHandler) collectInstalled(ctx context.Context) ([]string, error) {
	run := h.runCommand
	if run == nil {
		run = runIsolatedCommand
	}

	var formulae, casks []string
	var formulaeErr, casksErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		formulae, formulaeErr = run(ctx, "brew", "list", "--formula", "-1")
	}()
	go func() {
		defer wg.Done()
		casks, casksErr = run(ctx, "brew", "list", "--cask", "-1")
	}()
	wg.Wait()
	if formulaeErr != nil && casksErr != nil {
		return nil, errors.Join(formulaeErr, casksErr)
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
	return packages, nil
}
