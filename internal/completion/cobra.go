package completion

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CobraCompleter provides completions from Cobra-based CLI tools.
type CobraCompleter struct {
	cache    map[string]cachedResult
	cacheMu  sync.RWMutex
	cacheTTL time.Duration
}

type cachedResult struct {
	result    Result
	expiresAt time.Time
}

// NewCobraCompleter creates a new Cobra completer.
func NewCobraCompleter() *CobraCompleter {
	return &CobraCompleter{
		cache:    make(map[string]cachedResult),
		cacheTTL: 5 * time.Minute,
	}
}

// Name returns the completer name.
func (c *CobraCompleter) Name() string {
	return "cobra"
}

// Complete returns completions from Cobra __complete.
func (c *CobraCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	// Extract command and args
	parts := strings.Fields(line[:pos])
	if len(parts) == 0 {
		return Result{}, nil
	}

	cmdName := parts[0]

	// Check if command exists
	cmdPath, err := exec.LookPath(cmdName)
	if err != nil {
		return Result{}, nil
	}

	// Build __complete args
	// Cobra expects: cmd __complete <args...>
	args := append([]string{"__complete"}, parts[1:]...)

	// Add empty string if line ends with space (completing new word)
	if strings.HasSuffix(line[:pos], " ") {
		args = append(args, "")
	}

	// Check cache
	cacheKey := cmdPath + ":" + strings.Join(args, " ")
	c.cacheMu.RLock()
	if cached, ok := c.cache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		c.cacheMu.RUnlock()
		return cached.result, nil
	}
	c.cacheMu.RUnlock()

	// Run __complete
	cmd := exec.CommandContext(ctx, cmdPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		// Command might not support __complete
		return Result{}, err
	}

	// Parse output
	result := c.parseOutput(stdout.String())

	// Cache result
	c.cacheMu.Lock()
	c.cache[cacheKey] = cachedResult{
		result:    result,
		expiresAt: time.Now().Add(c.cacheTTL),
	}
	c.cacheMu.Unlock()

	return result, nil
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
}
