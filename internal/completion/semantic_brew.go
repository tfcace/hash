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

	inflightMu         sync.Mutex
	inflight           map[string]*brewInstalledCall
	inflightMaxWaitAge time.Duration
}

type brewInstalledCall struct {
	done     chan struct{}
	started  time.Time
	packages []string
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

	packages, err := h.lookupInstalled(queryCtx)
	if err != nil {
		return nil
	}
	if queryCtx.Err() != nil {
		return nil
	}
	if h.cacheTTL > 0 {
		h.cache.set("installed", packages, h.timeNow().Add(h.cacheTTL))
	}
	return packages
}

func (h *BrewHandler) lookupInstalled(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	const key = "installed"
	now := time.Now()
	h.inflightMu.Lock()
	if call, ok := h.inflight[key]; ok {
		if !contextReadCallIsStale(call.started, now, h.inflightMaxWaitAge) {
			h.inflightMu.Unlock()
			return waitForBrewInstalled(ctx, call)
		}
		delete(h.inflight, key)
	}

	call := &brewInstalledCall{done: make(chan struct{}), started: now}
	if h.inflight == nil {
		h.inflight = make(map[string]*brewInstalledCall)
	}
	h.inflight[key] = call
	h.inflightMu.Unlock()

	go h.finishInstalled(ctx, key, call)
	return waitForBrewInstalled(ctx, call)
}

func (h *BrewHandler) finishInstalled(ctx context.Context, key string, call *brewInstalledCall) {
	packages := h.collectInstalled(ctx)

	h.inflightMu.Lock()
	call.packages = packages
	if h.inflight[key] == call {
		delete(h.inflight, key)
	}
	h.inflightMu.Unlock()
	close(call.done)
}

func (h *BrewHandler) collectInstalled(ctx context.Context) []string {
	run := h.runCommand
	if run == nil {
		run = runIsolatedCommand
	}

	var formulae, casks []string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		lines, err := run(ctx, "brew", "list", "--formula", "-1")
		if err == nil {
			formulae = lines
		}
	}()
	go func() {
		defer wg.Done()
		lines, err := run(ctx, "brew", "list", "--cask", "-1")
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
	return packages
}

func waitForBrewInstalled(ctx context.Context, call *brewInstalledCall) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return append([]string(nil), call.packages...), nil
	}
}

func (h *BrewHandler) timeNow() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}
