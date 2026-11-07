package completion

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ExecutableCompleter completes executable names from PATH.
// It is triggered when completing the first word of a command (or after a pipe).
type ExecutableCompleter struct {
	builtins  []string
	functions FunctionProvider // nil: none
	policy    flightPolicy
	scans     flightCache[[]string] // keyed by $PATH with its relative entries resolved
	ready     readyNotifier
	scanPath  func(pathEnv string) []string
	readDir   func(string) ([]os.DirEntry, error)
	getwd     func() string // the shell's working directory, for relative $PATH entries
}

// NewExecutableCompleter creates a new executable completer.
func NewExecutableCompleter() *ExecutableCompleter {
	c := &ExecutableCompleter{
		policy: flightPolicy{
			ttl: 30 * time.Second,
			// An old listing is a fine answer while a rescan runs.
			staleGrace: 24 * time.Hour,
			// Directory reads cannot be canceled: a scan stuck on a dead
			// mount fails after this (the builtins are still offered) and
			// keeps the slot until it returns, so it is never doubled.
			timeout: 2 * time.Second,
			// A failed scan (panicked, or hung and then failed) is not
			// retried for this long.
			failureTTL: 2 * time.Second,
			// A cold Tab waits this long before reporting pending.
			wait: 50 * time.Millisecond,
		},
		readDir: os.ReadDir,
		getwd:   defaultGetwd,
	}
	c.scanPath = c.scanPATHExecutables
	c.scans.ready = &c.ready
	return c
}

// SetOnReady registers a callback fired whenever a PATH scan lands, so a
// "fetching" menu can fill itself in without another Tab.
func (c *ExecutableCompleter) SetOnReady(fn func()) {
	c.ready.set(fn)
}

// FunctionProvider is the interface for getting function names from the executor.
type FunctionProvider interface {
	Functions() []string
}

// SetFunctions registers where the user's functions and aliases come from.
// They are offered first in command position: run, they shadow builtins and
// PATH executables of the same name.
func (c *ExecutableCompleter) SetFunctions(p FunctionProvider) {
	c.functions = p
}

// SetBuiltins registers shell builtin names offered alongside PATH
// executables in command position, labeled so users can tell them apart.
func (c *ExecutableCompleter) SetBuiltins(names []string) {
	c.builtins = append([]string(nil), names...)
}

// Name returns the completer name.
func (c *ExecutableCompleter) Name() string {
	return "executable"
}

// Complete returns executable completions from PATH.
// Only completes when in command position (first word or after pipe).
func (c *ExecutableCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	// Only complete in command position: the first word of a command,
	// including after a pipe or other command separator.
	cw := parseCommandWords(line, pos)
	if !cw.inCommandPosition() {
		return Result{}, nil
	}
	prefix := cw.current

	// Don't complete if prefix contains a path separator (let file completer handle it)
	if strings.Contains(prefix, "/") {
		return Result{}, nil
	}

	// Get executables from PATH. Pending first: a scan still running will
	// notify ready even when the caller's deadline passed while waiting.
	executables, status := c.getExecutables(ctx)
	if status == lookupPending {
		return pendingResult(), nil
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	result, seen := c.shadowingItems(prefix)
	lowerPrefix := strings.ToLower(prefix)
	listed := 0
	for _, exe := range executables {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if seen[exe] || !strings.HasPrefix(strings.ToLower(exe), lowerPrefix) {
			continue
		}
		if listed == executableItemLimit {
			result.hidden.More++
			continue
		}
		listed++
		result.Items = append(result.Items, Item{Value: exe, Display: exe, Icon: ""})
	}
	return result, nil
}

// executableItemLimit is how many PATH executables a menu lists, after the
// functions and builtins.
const executableItemLimit = 50

// shadowingItems returns the functions (matched case-sensitively, as the
// shell defines them) and builtins matching the prefix, labeled so they
// stand out from PATH executables. They come first: they shadow
// same-named executables when executed, functions shadow builtins too.
func (c *ExecutableCompleter) shadowingItems(prefix string) (result Result, seen map[string]bool) {
	seen = make(map[string]bool, len(c.builtins))
	add := func(name, description, icon string) {
		if !seen[name] && result.add(Item{Value: name, Display: name, Description: description, Icon: icon}) {
			seen[name] = true
		}
	}
	if c.functions != nil {
		var functions []string
		for _, name := range c.functions.Functions() {
			if strings.HasPrefix(name, prefix) {
				functions = append(functions, name)
			}
		}
		slices.Sort(functions) // the shell keeps them in a map
		for _, name := range functions {
			add(name, "function", "ƒ")
		}
	}
	lowerPrefix := strings.ToLower(prefix)
	for _, name := range c.builtins {
		if strings.HasPrefix(strings.ToLower(name), lowerPrefix) {
			add(name, "hash builtin", "")
		}
	}
	return result, seen
}

// getExecutables returns all executables in PATH, using a cache. The
// returned slice is shared with the cache and must not be modified.
func (c *ExecutableCompleter) getExecutables(ctx context.Context) ([]string, lookupStatus) {
	pathEnv := absolutePath(os.Getenv("PATH"), c.getwd)
	scan := c.scanPath
	if scan == nil {
		scan = c.scanPATHExecutables
	}
	return c.scans.get(ctx, pathEnv, c.policy, func(context.Context) ([]string, error) {
		return scan(pathEnv), nil
	})
}

// absolutePath returns pathEnv with its relative entries ("" and "." are
// the working directory) made absolute against getwd(), so a scan and its
// cache key name the directories searched now, not those of a directory
// the shell has left. getwd is asked only when an entry is relative.
func absolutePath(pathEnv string, getwd func() string) string {
	dirs := filepath.SplitList(pathEnv)
	wd := ""
	for i, dir := range dirs {
		if filepath.IsAbs(dir) {
			continue
		}
		if wd == "" {
			wd = getwd()
		}
		dirs[i] = filepath.Join(wd, dir)
	}
	if wd == "" {
		return pathEnv
	}
	return strings.Join(dirs, string(filepath.ListSeparator))
}

func (c *ExecutableCompleter) scanPATHExecutables(pathEnv string) []string {
	seen := make(map[string]bool)
	var executables []string

	for _, dir := range filepath.SplitList(pathEnv) {
		entries, err := c.readDir(dir)
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
