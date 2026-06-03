package completion

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// CobraCompleter provides completions from Cobra-based CLI tools.
// It uses background prefetching to avoid blocking on TAB press.
type CobraCompleter struct {
	cache      map[string]cachedResult
	cacheMu    sync.RWMutex
	cacheTTL   time.Duration
	prefetched map[string]bool // tracks prefetch attempts (including failures)
	prefetchMu sync.RWMutex

	lookPathCache   map[string]string // command name → resolved path
	lookPathCacheMu sync.RWMutex
}

type cachedResult struct {
	result    Result
	expiresAt time.Time
}

// NewCobraCompleter creates a new Cobra completer.
func NewCobraCompleter() *CobraCompleter {
	return &CobraCompleter{
		cache:         make(map[string]cachedResult),
		cacheTTL:      5 * time.Minute,
		prefetched:    make(map[string]bool),
		lookPathCache: make(map[string]string),
	}
}

// Name returns the completer name.
func (c *CobraCompleter) Name() string {
	return "cobra"
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

	p, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}

	c.lookPathCacheMu.Lock()
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

	// Only return cached results - never block
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()

	if cached, ok := c.cache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		return cached.result, nil
	}

	return Result{}, nil
}

// Prefetch triggers background fetching of Cobra completions.
// Call this when the user types a space after a command.
func (c *CobraCompleter) Prefetch(line string, pos int) {
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

	// Check if already prefetching or recently failed
	prefetchKey := cmdName + ":" + strings.Join(args, " ")
	c.prefetchMu.Lock()
	if c.prefetched[prefetchKey] {
		c.prefetchMu.Unlock()
		return
	}
	c.prefetched[prefetchKey] = true
	c.prefetchMu.Unlock()

	// Run prefetch in background
	go c.prefetchCommand(cmdName, args)
}

func (c *CobraCompleter) prefetchCommand(cmdName string, args []string) {
	cmdPath, err := c.lookPath(cmdName)
	if err != nil {
		return
	}

	cacheKey := cmdPath + ":" + strings.Join(args, " ")

	// Check if already cached
	c.cacheMu.RLock()
	if cached, ok := c.cache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		c.cacheMu.RUnlock()
		return
	}
	c.cacheMu.RUnlock()

	c.doPrefetch(cmdPath, args, cacheKey)
}

// doPrefetch runs the actual Cobra completion in the background.
func (c *CobraCompleter) doPrefetch(cmdPath string, args []string, cacheKey string) {
	// Use short timeout - Cobra completions should be fast
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Completely isolate the process from our terminal:
	// - Setsid creates a new session, detaching from controlling terminal
	// - Stdin from /dev/null prevents reading
	// This prevents TUI apps (vim, helix) from taking over or corrupting our terminal
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	devNull, err := os.Open(os.DevNull)
	if err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	err = cmd.Run()
	if err != nil {
		// Command doesn't support __complete or timed out
		// Mark as prefetched so we don't retry
		return
	}

	// Parse output
	result := c.parseOutput(stdout.String())

	// Cache result (even empty results to avoid re-fetching)
	c.cacheMu.Lock()
	c.cache[cacheKey] = cachedResult{
		result:    result,
		expiresAt: time.Now().Add(c.cacheTTL),
	}
	c.cacheMu.Unlock()
}

// parseOutput parses Cobra __complete output.
// Format: one completion per line, with optional :description suffix
func (c *CobraCompleter) parseOutput(output string) Result {
	var items []Item

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip directive lines (start with :)
		if strings.HasPrefix(line, ":") {
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
	}

	return Result{Items: items}
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
	c.prefetched = make(map[string]bool)
	c.prefetchMu.Unlock()

	c.lookPathCacheMu.Lock()
	c.lookPathCache = make(map[string]string)
	c.lookPathCacheMu.Unlock()
}
