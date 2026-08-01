package completion

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CobraCompleter provides completions from Cobra-based CLI tools.
// It uses background prefetching to avoid blocking on TAB press.
type CobraCompleter struct {
	cache           map[string]cachedResult
	cacheMu         sync.RWMutex
	cacheTTL        time.Duration
	prefetchTimeout time.Duration
	prefetched      map[string]time.Time // prefetch key -> next retry time
	prefetchMu      sync.RWMutex

	resolvePath      func(string) (string, error)
	lookPathCache    map[string]string // command name → resolved path
	lookPathInFlight map[string]*cobraLookPathCall
	lookPathMaxAge   time.Duration
	lookPathCacheMu  sync.RWMutex

	supportsMu sync.RWMutex
	supports   map[string]bool // cmdPath → tool has answered __complete before

	readyMu sync.RWMutex
	onReady func()
}

type cobraLookPathCall struct {
	started time.Time
}

type cachedResult struct {
	result    Result
	expiresAt time.Time
}

// cobraPrefetchTimeout bounds a background fetch, not the TAB the user is
// waiting on, so it can afford to be generous. A cold kubectl __complete
// takes 150-500ms; killing it early caches nothing and fails identically on
// every retry, which shows up as completion "randomly" needing several
// attempts to start working.
const cobraPrefetchTimeout = 3 * time.Second
const cobraFailedPrefetchTTL = 2 * time.Second

// knownCobraCommands seeds tools that are widely known to answer
// __complete, so their very first cache miss in a session reports pending
// (fetching notice, menu opens itself) instead of flashing an unrelated
// filename menu once. A wrong entry self-corrects: the fetch fails, the
// failure is cached briefly, and completion falls through as before.
var knownCobraCommands = map[string]bool{
	"kubectl":  true,
	"helm":     true,
	"gh":       true,
	"docker":   true,
	"podman":   true,
	"minikube": true,
	"kind":     true,
}

var errCobraLookPathBusy = errors.New("cobra command path lookup already in progress")

// NewCobraCompleter creates a new Cobra completer.
func NewCobraCompleter() *CobraCompleter {
	return &CobraCompleter{
		cache:            make(map[string]cachedResult),
		cacheTTL:         5 * time.Minute,
		prefetchTimeout:  cobraPrefetchTimeout,
		prefetched:       make(map[string]time.Time),
		resolvePath:      exec.LookPath,
		lookPathCache:    make(map[string]string),
		lookPathInFlight: make(map[string]*cobraLookPathCall),
		lookPathMaxAge:   contextReadInflightMaxWaitAge,
	}
}

// Name returns the completer name.
func (c *CobraCompleter) Name() string {
	return "cobra"
}

// SetOnReady registers a callback fired whenever a background fetch finishes
// and fills the cache. The UI uses it to refresh a "fetching" menu without
// another TAB.
func (c *CobraCompleter) SetOnReady(fn func()) {
	c.readyMu.Lock()
	c.onReady = fn
	c.readyMu.Unlock()
}

func (c *CobraCompleter) notifyReady() {
	c.readyMu.RLock()
	fn := c.onReady
	c.readyMu.RUnlock()
	if fn != nil {
		fn()
	}
}

func (c *CobraCompleter) markSupportsComplete(cmdPath string) {
	c.supportsMu.Lock()
	if c.supports == nil {
		c.supports = make(map[string]bool)
	}
	c.supports[cmdPath] = true
	c.supportsMu.Unlock()
}

func (c *CobraCompleter) supportsComplete(cmdPath string) bool {
	c.supportsMu.RLock()
	defer c.supportsMu.RUnlock()
	return c.supports[cmdPath]
}

// lookPath returns the resolved path for a command, using a cache to avoid
// repeated PATH scans on every TAB press.
func (c *CobraCompleter) lookPath(name string) (string, error) {
	c.lookPathCacheMu.RLock()
	if p, ok := c.lookPathCache[name]; ok {
		c.lookPathCacheMu.RUnlock()
		return p, nil
	}
	c.lookPathCacheMu.RUnlock()

	c.lookPathCacheMu.Lock()
	if p, ok := c.lookPathCache[name]; ok {
		c.lookPathCacheMu.Unlock()
		return p, nil
	}
	if c.lookPathInFlight == nil {
		c.lookPathInFlight = make(map[string]*cobraLookPathCall)
	}
	now := time.Now()
	if call, ok := c.lookPathInFlight[name]; ok {
		maxAge := c.lookPathMaxAge
		if maxAge == 0 {
			maxAge = contextReadInflightMaxWaitAge
		}
		if maxAge < 0 || now.Sub(call.started) < maxAge {
			c.lookPathCacheMu.Unlock()
			return "", errCobraLookPathBusy
		}
		delete(c.lookPathInFlight, name)
	}
	call := &cobraLookPathCall{started: now}
	c.lookPathInFlight[name] = call
	c.lookPathCacheMu.Unlock()

	resolve := c.resolvePath
	if resolve == nil {
		resolve = exec.LookPath
	}
	p, err := resolve(name)

	c.lookPathCacheMu.Lock()
	if c.lookPathInFlight[name] != call {
		c.lookPathCacheMu.Unlock()
		return "", errCobraLookPathBusy
	}
	delete(c.lookPathInFlight, name)
	if err != nil {
		c.lookPathCacheMu.Unlock()
		return "", err
	}
	c.lookPathCache[name] = p
	c.lookPathCacheMu.Unlock()
	return p, nil
}

func (c *CobraCompleter) cachedPath(name string) (string, bool) {
	c.lookPathCacheMu.RLock()
	defer c.lookPathCacheMu.RUnlock()
	p, ok := c.lookPathCache[name]
	return p, ok
}

// Complete returns completions from cache only.
// Use Prefetch to populate the cache in the background.
func (c *CobraCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	pos = clampCursor(line, pos)

	// Extract pipe context - get command segment after last pipe
	pipeLine, pipePos := ExtractPipeContext(line, pos)

	// Extract command and args from the pipe context
	parts := strings.Fields(pipeLine[:pipePos])
	if len(parts) == 0 {
		return Result{}, nil
	}

	cmdName := parts[0]
	if isShellBuiltinForCobra(cmdName) {
		return Result{}, nil
	}

	// "$VAR" and "~/..." words belong to the env and file completers; a
	// pending (or cached) cobra answer must not shadow them.
	if len(parts) > 1 && !strings.HasSuffix(line[:pos], " ") {
		if w := parts[len(parts)-1]; strings.HasPrefix(w, "$") || strings.HasPrefix(w, "~") {
			return Result{}, nil
		}
	}

	// Complete must be cache-only. PATH scans can block on slow mounts and
	// must stay in background prefetch.
	cmdPath, ok := c.cachedPath(cmdName)
	if !ok {
		return Result{}, nil
	}

	// Build cache key
	args := append([]string{"__complete"}, parts[1:]...)
	if strings.HasSuffix(line[:pos], " ") {
		args = append(args, "")
	}
	cacheKey := cmdPath + ":" + strings.Join(args, " ")

	// Only return cached results - never block.
	c.cacheMu.RLock()
	cached, ok := c.cache[cacheKey]
	c.cacheMu.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.result, nil
	}

	// Miss: always start the background fetch (deduplicated), so the next
	// TAB can answer even when no space-triggered prefetch covered this key.
	c.startPrefetch(cmdName, args)

	// For a tool that answers __complete (learned this session, or seeded),
	// falling through would answer a subcommand argument with filenames, so
	// report pending; the ready notification reopens the menu when the data
	// lands. Unknown tools keep the fall-through: pending would wrongly
	// block file completion for them.
	if c.supportsComplete(cmdPath) || knownCobraCommands[cmdName] {
		return Result{Pending: true}, nil
	}
	return Result{}, nil
}

// Prefetch triggers background fetching of Cobra completions.
// Call this when the user types a space after a command.
func (c *CobraCompleter) Prefetch(line string, pos int) {
	pos = clampCursor(line, pos)

	// Extract pipe context - get command segment after last pipe
	pipeLine, pipePos := ExtractPipeContext(line, pos)

	// Extract command and args from the pipe context
	parts := strings.Fields(pipeLine[:pipePos])
	if len(parts) == 0 {
		return
	}

	cmdName := parts[0]
	if isShellBuiltinForCobra(cmdName) {
		return
	}

	// Build cache key
	args := append([]string{"__complete"}, parts[1:]...)
	if strings.HasSuffix(line[:pos], " ") {
		args = append(args, "")
	}

	c.startPrefetch(cmdName, args)
}

// startPrefetch launches a background fetch unless one for the same key is
// already running or recently failed.
func (c *CobraCompleter) startPrefetch(cmdName string, args []string) {
	prefetchKey := cmdName + ":" + strings.Join(args, " ")
	now := time.Now()
	c.prefetchMu.Lock()
	if retryAt, ok := c.prefetched[prefetchKey]; ok && now.Before(retryAt) {
		c.prefetchMu.Unlock()
		return
	}
	c.prefetched[prefetchKey] = c.nextFailedPrefetchRetry(now)
	c.prefetchMu.Unlock()

	// Run prefetch in background
	go c.prefetchCommand(prefetchKey, cmdName, args)
}

func (c *CobraCompleter) prefetchCommand(prefetchKey, cmdName string, args []string) {
	cmdPath, err := c.lookPath(cmdName)
	if err != nil {
		if errors.Is(err, errCobraLookPathBusy) {
			c.forgetPrefetch(prefetchKey)
		}
		return
	}

	cacheKey := cmdPath + ":" + strings.Join(args, " ")

	// Check if already cached
	c.cacheMu.RLock()
	if cached, ok := c.cache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		c.cacheMu.RUnlock()
		c.markPrefetchedUntil(prefetchKey, cached.expiresAt)
		return
	}
	c.cacheMu.RUnlock()

	c.doPrefetch(cmdPath, args, cacheKey)
}

func (c *CobraCompleter) forgetPrefetch(prefetchKey string) {
	c.prefetchMu.Lock()
	delete(c.prefetched, prefetchKey)
	c.prefetchMu.Unlock()
}

func (c *CobraCompleter) markPrefetchedUntil(prefetchKey string, retryAt time.Time) {
	c.prefetchMu.Lock()
	c.prefetched[prefetchKey] = retryAt
	c.prefetchMu.Unlock()
}

func (c *CobraCompleter) nextFailedPrefetchRetry(now time.Time) time.Time {
	ttl := cobraFailedPrefetchTTL
	if c.cacheTTL > 0 && c.cacheTTL < ttl {
		ttl = c.cacheTTL
	}
	return now.Add(ttl)
}

// doPrefetch runs the actual Cobra completion in the background.
func (c *CobraCompleter) doPrefetch(cmdPath string, args []string, cacheKey string) {
	// Use short timeout - Cobra completions should be fast
	ctx, cancel := context.WithTimeout(context.Background(), c.prefetchTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Completely isolate the process from our terminal:
	// - Setsid creates a new session, detaching from controlling terminal
	// - Stdin from /dev/null prevents reading
	// This prevents TUI apps (vim, helix) from taking over or corrupting our terminal
	configureIsolatedCompletionCommand(cmd)
	devNull, err := os.Open(os.DevNull)
	if err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	err = cmd.Run()
	if err != nil {
		// Command doesn't support __complete or timed out. Cache the empty
		// result briefly so a pending Complete stops re-reporting pending,
		// and notify so a "fetching" menu can clear itself and fall back.
		c.cacheFailedPrefetch(cacheKey)
		return
	}

	// A successful exit alone does not prove Cobra support: ordinary commands
	// such as echo also accept arbitrary arguments. Cobra's __complete protocol
	// always terminates stdout with a numeric directive line.
	result, valid := c.parseOutput(stdout.String())
	if !valid {
		c.cacheFailedPrefetch(cacheKey)
		return
	}

	// The tool answered __complete: cache misses for it may now report
	// pending instead of falling through to unrelated completers.
	c.markSupportsComplete(cmdPath)

	// Cache result (even empty results to avoid re-fetching)
	c.cacheMu.Lock()
	c.cache[cacheKey] = cachedResult{
		result:    result,
		expiresAt: time.Now().Add(c.cacheTTL),
	}
	c.cacheMu.Unlock()
	c.notifyReady()
}

func (c *CobraCompleter) cacheFailedPrefetch(cacheKey string) {
	c.cacheMu.Lock()
	c.cache[cacheKey] = cachedResult{expiresAt: time.Now().Add(cobraFailedPrefetchTTL)}
	c.cacheMu.Unlock()
	c.notifyReady()
}

// parseOutput parses Cobra __complete output.
// Format: one completion per line, with an optional tab-separated description,
// followed by a final :<unsigned integer> directive line.
func (c *CobraCompleter) parseOutput(output string) (Result, bool) {
	var items []Item

	lines := strings.Split(output, "\n")
	lastNonEmpty := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastNonEmpty = i
			break
		}
	}
	if lastNonEmpty < 0 || !isCobraDirectiveLine(strings.TrimSuffix(lines[lastNonEmpty], "\r")) {
		return Result{}, false
	}

	for i, line := range lines {
		if i == lastNonEmpty {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse completion:description format
		parts := strings.SplitN(line, "\t", 2)
		value := parts[0]
		var desc string
		if len(parts) > 1 {
			desc = parts[1]
		}

		items = append(items, Item{
			Value:       value,
			Display:     value,
			Description: desc,
		})
		if len(items) >= completionItemLimit {
			break
		}
	}

	return Result{Items: items}, true
}

func isCobraDirectiveLine(line string) bool {
	if len(line) < 2 || line[0] != ':' {
		return false
	}
	for _, r := range line[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isShellBuiltinForCobra(cmd string) bool {
	switch cmd {
	case "cd", "pushd", "popd", "exit", "quit", "history", "copy", "issue", "status", "tips", "setup-zoxide":
		return true
	case "alias", "unalias", "export", "unset", "source", ".":
		return true
	default:
		return false
	}
}

// SetCacheTTL sets the cache TTL.
func (c *CobraCompleter) SetCacheTTL(ttl time.Duration) {
	c.cacheTTL = ttl
}

// ClearCache clears the completion cache.
func (c *CobraCompleter) ClearCache() {
	c.cacheMu.Lock()
	c.cache = make(map[string]cachedResult)
	c.cacheMu.Unlock()

	c.prefetchMu.Lock()
	c.prefetched = make(map[string]time.Time)
	c.prefetchMu.Unlock()

	c.lookPathCacheMu.Lock()
	c.lookPathCache = make(map[string]string)
	c.lookPathCacheMu.Unlock()
}
