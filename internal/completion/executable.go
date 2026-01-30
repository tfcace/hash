package completion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ExecutableCompleter completes executable names from PATH.
// It is triggered when completing the first word of a command (or after a pipe).
type ExecutableCompleter struct {
	cache     []string
	cacheTime time.Time
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration
}

// NewExecutableCompleter creates a new executable completer.
func NewExecutableCompleter() *ExecutableCompleter {
	return &ExecutableCompleter{
		cacheTTL: 30 * time.Second,
	}
}

// Name returns the completer name.
func (c *ExecutableCompleter) Name() string {
	return "executable"
}

// Complete returns executable completions from PATH.
// Only completes when in command position (first word or after pipe).
func (c *ExecutableCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	// Extract pipe context to handle commands after pipes
	pipeLine, pipePos := ExtractPipeContext(line, pos)

	// Only complete if we're in command position (first word)
	parts := strings.Fields(pipeLine[:pipePos])

	// Check if we're completing the first word (command position)
	// We're in command position if:
	// 1. No parts yet (empty line)
	// 2. One part and no trailing space (still typing command)
	isCommandPosition := len(parts) == 0 || (len(parts) == 1 && !strings.HasSuffix(pipeLine[:pipePos], " "))

	if !isCommandPosition {
		return Result{}, nil
	}

	// Get the prefix we're completing
	prefix := ""
	if len(parts) == 1 {
		prefix = parts[0]
	}

	// Don't complete if prefix contains a path separator (let file completer handle it)
	if strings.Contains(prefix, "/") {
		return Result{}, nil
	}

	// Get executables from PATH
	executables := c.getExecutables()

	var items []Item
	lowerPrefix := strings.ToLower(prefix)
	for _, exe := range executables {
		if prefix == "" || strings.HasPrefix(strings.ToLower(exe), lowerPrefix) {
			items = append(items, Item{
				Value:   exe,
				Display: exe,
				Icon:    "",
			})
		}
	}

	// Limit results to avoid overwhelming the UI
	if len(items) > 50 {
		items = items[:50]
	}

	return Result{Items: items}, nil
}

// getExecutables returns all executables in PATH, using a cache.
func (c *ExecutableCompleter) getExecutables() []string {
	c.cacheMu.RLock()
	if c.cache != nil && time.Since(c.cacheTime) < c.cacheTTL {
		result := c.cache
		c.cacheMu.RUnlock()
		return result
	}
	c.cacheMu.RUnlock()

	// Rebuild cache
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	// Double-check after acquiring write lock
	if c.cache != nil && time.Since(c.cacheTime) < c.cacheTTL {
		return c.cache
	}

	seen := make(map[string]bool)
	var executables []string

	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			if seen[name] {
				continue
			}

			// Check if executable
			info, err := entry.Info()
			if err != nil {
				continue
			}

			// On Unix, check executable bit
			if info.Mode()&0o111 != 0 {
				seen[name] = true
				executables = append(executables, name)
			}
		}
	}

	c.cache = executables
	c.cacheTime = time.Now()
	return executables
}
