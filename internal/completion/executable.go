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
	cache           []string
	cacheTime       time.Time
	cacheMu         sync.RWMutex
	cacheTTL        time.Duration
	refreshMu       sync.Mutex
	refreshing      bool
	refreshStarted  time.Time
	refreshDone     chan struct{}
	refreshMaxAge   time.Duration
	coldScanWait    time.Duration
	scanExecutables func() []string
	readDir         func(string) ([]os.DirEntry, error)
}

// NewExecutableCompleter creates a new executable completer.
func NewExecutableCompleter() *ExecutableCompleter {
	c := &ExecutableCompleter{
		cacheTTL:      30 * time.Second,
		refreshMaxAge: contextReadInflightMaxWaitAge,
		coldScanWait:  50 * time.Millisecond,
		readDir:       os.ReadDir,
	}
	c.scanExecutables = c.scanPATHExecutables
	return c
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
	executables := c.getExecutables(ctx)
	if ctx.Err() != nil {
		return Result{}, nil
	}

	items := make([]Item, 0, min(len(executables), 50))
	lowerPrefix := strings.ToLower(prefix)
	for _, exe := range executables {
		if ctx.Err() != nil {
			return Result{}, nil
		}
		if prefix == "" || strings.HasPrefix(strings.ToLower(exe), lowerPrefix) {
			items = append(items, Item{
				Value:   exe,
				Display: exe,
				Icon:    "",
			})
			if len(items) >= 50 {
				break
			}
		}
	}

	return Result{Items: items}, nil
}

// getExecutables returns all executables in PATH, using a cache.
func (c *ExecutableCompleter) getExecutables(ctx context.Context) []string {
	executables, fresh := c.cacheSnapshot()
	if fresh {
		return executables
	}

	done := c.refreshAsync()
	if len(executables) > 0 || c.coldScanWait <= 0 {
		return executables
	}

	timer := time.NewTimer(c.coldScanWait)
	defer timer.Stop()

	select {
	case <-done:
		executables, _ = c.cacheSnapshot()
		return executables
	case <-ctx.Done():
		executables, _ = c.cacheSnapshot()
		return executables
	case <-timer.C:
		executables, _ = c.cacheSnapshot()
		return executables
	}
}

func (c *ExecutableCompleter) cacheSnapshot() ([]string, bool) {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	return c.cache, c.cache != nil && time.Since(c.cacheTime) < c.cacheTTL
}

func (c *ExecutableCompleter) refreshAsync() <-chan struct{} {
	c.refreshMu.Lock()
	if c.refreshing {
		maxAge := c.refreshMaxAge
		if maxAge == 0 {
			maxAge = contextReadInflightMaxWaitAge
		}
		if maxAge < 0 || time.Since(c.refreshStarted) < maxAge {
			done := c.refreshDone
			c.refreshMu.Unlock()
			return done
		}
	}

	done := make(chan struct{})
	c.refreshing = true
	c.refreshStarted = time.Now()
	c.refreshDone = done
	scan := c.scanExecutables
	if scan == nil {
		scan = c.scanPATHExecutables
	}
	c.refreshMu.Unlock()

	go func() {
		executables := scan()

		c.refreshMu.Lock()
		if c.refreshDone != done {
			c.refreshMu.Unlock()
			close(done)
			return
		}

		c.cacheMu.Lock()
		c.cache = executables
		c.cacheTime = time.Now()
		c.cacheMu.Unlock()

		c.refreshing = false
		c.refreshMu.Unlock()
		close(done)
	}()

	return done
}

func (c *ExecutableCompleter) scanPATHExecutables() []string {
	seen := make(map[string]bool)
	var executables []string

	pathEnv := os.Getenv("PATH")
	readDir := c.readDir
	if readDir == nil {
		readDir = os.ReadDir
	}

	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}

		entries, err := readDir(dir)
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

			// Stay syscall-light: avoid Info/Stat here because PATH entries may
			// live on slow mounts. PATH directories are expected to contain
			// commands, so type filtering is enough for interactive completion.
			typ := entry.Type()
			if typ&^(os.ModeSymlink) != 0 {
				continue
			}

			seen[name] = true
			executables = append(executables, name)
		}
	}

	return executables
}
