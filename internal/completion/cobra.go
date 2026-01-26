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
}

type cachedResult struct {
	result    Result
	expiresAt time.Time
}

// NewCobraCompleter creates a new Cobra completer.
func NewCobraCompleter() *CobraCompleter {
	return &CobraCompleter{
		cache:      make(map[string]cachedResult),
		cacheTTL:   5 * time.Minute,
		prefetched: make(map[string]bool),
	}
}

// Name returns the completer name.
func (c *CobraCompleter) Name() string {
	return "cobra"
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

	// Check if command exists
	cmdPath, err := exec.LookPath(cmdName)
	if err != nil {
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

	// Check if command exists
	cmdPath, err := exec.LookPath(cmdName)
	if err != nil {
		return
	}

	// Build cache key
	args := append([]string{"__complete"}, parts[1:]...)
	if strings.HasSuffix(line[:pos], " ") {
		args = append(args, "")
	}
	cacheKey := cmdPath + ":" + strings.Join(args, " ")

	// Check if already cached
	c.cacheMu.RLock()
	if cached, ok := c.cache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		c.cacheMu.RUnlock()
		return
	}
	c.cacheMu.RUnlock()

	// Check if already prefetching or recently failed
	c.prefetchMu.Lock()
	if c.prefetched[cacheKey] {
		c.prefetchMu.Unlock()
		return
	}
	c.prefetched[cacheKey] = true
	c.prefetchMu.Unlock()

	// Run prefetch in background
	go c.doPrefetch(cmdPath, args, cacheKey)
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
}
