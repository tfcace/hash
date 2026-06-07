package completion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tfcace/hash/internal/trace"
)

// FileCompleter completes filesystem paths.
type FileCompleter struct {
	showHidden   bool
	fuzzyMode    bool
	readDir      func(string) ([]os.DirEntry, error)
	readCacheTTL time.Duration
	readMu       sync.Mutex
	readInflight map[string]*fileReadCall
	readCache    map[string]fileReadCacheEntry
}

type fileReadResult struct {
	entries []os.DirEntry
	err     error
}

type fileReadCall struct {
	done   chan struct{}
	result fileReadResult
}

type fileReadCacheEntry struct {
	result    fileReadResult
	expiresAt time.Time
}

// NewFileCompleter creates a new filesystem completer.
func NewFileCompleter() *FileCompleter {
	return &FileCompleter{
		showHidden:   false,
		fuzzyMode:    false,
		readDir:      os.ReadDir,
		readCacheTTL: 500 * time.Millisecond,
	}
}

// SetFuzzyMode sets whether to return all candidates (for router-level fuzzy filtering).
func (c *FileCompleter) SetFuzzyMode(enabled bool) {
	c.fuzzyMode = enabled
}

// Name returns the completer name.
func (c *FileCompleter) Name() string {
	return "file"
}

// SetShowHidden toggles hidden file visibility.
func (c *FileCompleter) SetShowHidden(show bool) {
	c.showHidden = show
}

// Complete returns filesystem completions.
//
//nolint:gocyclo // file completion handles multiple path formats and edge cases
func (c *FileCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	start := time.Now()
	traceEnabled := trace.Enabled("completion")

	// Extract the word being completed
	word := shellUnescapeWord(shellWordAt(line, pos))
	originalWord := word // Save for hidden file detection
	if word == "" {
		word = "."
	}

	// Expand tilde
	expandedWord := expandTilde(word)

	// Determine directory and prefix to match
	dir := filepath.Dir(expandedWord)
	prefix := filepath.Base(expandedWord)

	// If word ends with /, list that directory
	if strings.HasSuffix(word, "/") || strings.HasSuffix(word, string(os.PathSeparator)) {
		dir = expandedWord
		prefix = ""
	}

	// Handle "." specially
	if word == "." || word == "" {
		dir = "."
		prefix = ""
	}

	// Check if the command only accepts directories (cd, pushd, popd, rmdir)
	dirsOnly := isDirOnlyCommand(line, pos)
	if traceEnabled {
		trace.Emit("completion", "file_start", trace.LevelDetailed, map[string]any{
			"line":       line,
			"pos":        pos,
			"word":       originalWord,
			"dir":        dir,
			"prefix":     prefix,
			"dirs_only":  dirsOnly,
			"fuzzy_mode": c.fuzzyMode,
		})
	}

	readStart := time.Now()
	readResult, cached, ok := c.readDirectory(ctx, dir)
	if !ok {
		if traceEnabled {
			trace.Emit("completion", "file_readdir_canceled", trace.LevelDetailed, map[string]any{
				"dir":         dir,
				"duration_ms": float64(time.Since(readStart).Microseconds()) / 1000.0,
			})
		}
		return Result{}, nil
	}
	if traceEnabled {
		errText := ""
		if readResult.err != nil {
			errText = readResult.err.Error()
		}
		trace.Emit("completion", "file_readdir_done", trace.LevelDetailed, map[string]any{
			"dir":         dir,
			"entries":     len(readResult.entries),
			"cached":      cached,
			"error":       errText,
			"duration_ms": float64(time.Since(readStart).Microseconds()) / 1000.0,
		})
	}
	if readResult.err != nil {
		return Result{}, nil //nolint:nilerr // graceful degradation: return empty on unreadable dir
	}
	entries := readResult.entries

	// Show hidden files if user is explicitly typing a dot prefix
	// Use originalWord to avoid false positive when word defaults to "."
	wantsDotFiles := originalWord != "" && strings.HasPrefix(filepath.Base(originalWord), ".")
	showHidden := c.showHidden || wantsDotFiles

	var items []Item
	var hiddenSkipped, prefixSkipped, nonDirSkipped, symlinkAssumed int
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			if traceEnabled {
				trace.Emit("completion", "file_canceled", trace.LevelDetailed, map[string]any{
					"items":       len(items),
					"duration_ms": float64(time.Since(start).Microseconds()) / 1000.0,
				})
			}
			return Result{}, nil
		default:
		}

		name := entry.Name()

		// Skip hidden files unless enabled or prefix starts with "."
		if !showHidden && strings.HasPrefix(name, ".") {
			hiddenSkipped++
			continue
		}

		// Skip if doesn't match prefix (unless fuzzy mode - let router filter)
		if !c.fuzzyMode && prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			prefixSkipped++
			continue
		}

		// Build completion value
		value := name
		isDir := entry.IsDir()
		isSymlink := entry.Type()&os.ModeSymlink != 0
		// For directory-only commands, avoid following symlinks. Network or
		// cloud-backed symlink targets can block path completion.
		if dirsOnly && isSymlink {
			isDir = true
			symlinkAssumed++
		}
		if isDir {
			value += "/"
		}

		// Skip non-directories for directory-only commands
		if dirsOnly && !isDir {
			nonDirSkipped++
			continue
		}

		items = append(items, Item{
			Value:       value,
			Display:     name,
			Icon:        getFileIcon(entry),
			Description: fileDescription(entry, isDir),
		})
	}

	rawPrefix := getCompletionPrefix(originalWord, prefix)
	for i := range items {
		items[i].Value = EscapeShellWord(items[i].Value)
	}
	if traceEnabled {
		trace.Emit("completion", "file_done", trace.LevelDetailed, map[string]any{
			"entries":          len(entries),
			"items":            len(items),
			"hidden_skipped":   hiddenSkipped,
			"prefix_skipped":   prefixSkipped,
			"non_dir_skipped":  nonDirSkipped,
			"symlink_assumed":  symlinkAssumed,
			"duration_ms":      float64(time.Since(start).Microseconds()) / 1000.0,
			"context_canceled": ctx.Err() != nil,
		})
	}

	return Result{
		Items:  items,
		Prefix: EscapeShellWord(rawPrefix),
	}, nil
}

func (c *FileCompleter) readDirectory(ctx context.Context, dir string) (fileReadResult, bool, bool) {
	cacheKey := fileReadCacheKey(dir)
	now := time.Now()

	c.readMu.Lock()
	if c.readCacheTTL > 0 && c.readCache != nil {
		if cached, ok := c.readCache[cacheKey]; ok && now.Before(cached.expiresAt) {
			result := cloneFileReadResult(cached.result)
			c.readMu.Unlock()
			return result, true, true
		}
	}
	if call, ok := c.readInflight[cacheKey]; ok {
		c.readMu.Unlock()
		select {
		case <-ctx.Done():
			return fileReadResult{}, false, false
		case <-call.done:
			return cloneFileReadResult(call.result), false, true
		}
	}

	call := &fileReadCall{done: make(chan struct{})}
	if c.readInflight == nil {
		c.readInflight = make(map[string]*fileReadCall)
	}
	c.readInflight[cacheKey] = call
	readDir := c.readDir
	if readDir == nil {
		readDir = os.ReadDir
	}
	c.readMu.Unlock()

	go c.finishDirectoryRead(cacheKey, dir, readDir, call)

	select {
	case <-ctx.Done():
		return fileReadResult{}, false, false
	case <-call.done:
		return cloneFileReadResult(call.result), false, true
	}
}

func (c *FileCompleter) finishDirectoryRead(
	cacheKey string,
	dir string,
	readDir func(string) ([]os.DirEntry, error),
	call *fileReadCall,
) {
	entries, err := readDir(dir)
	result := fileReadResult{entries: entries, err: err}

	c.readMu.Lock()
	call.result = result
	if err == nil && c.readCacheTTL > 0 {
		if c.readCache == nil {
			c.readCache = make(map[string]fileReadCacheEntry)
		}
		c.readCache[cacheKey] = fileReadCacheEntry{
			result:    cloneFileReadResult(result),
			expiresAt: time.Now().Add(c.readCacheTTL),
		}
	}
	delete(c.readInflight, cacheKey)
	c.readMu.Unlock()
	close(call.done)
}

func fileReadCacheKey(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func cloneFileReadResult(result fileReadResult) fileReadResult {
	return fileReadResult{
		entries: append([]os.DirEntry(nil), result.entries...),
		err:     result.err,
	}
}

// isDirOnlyCommand checks if the command on the line only accepts directories.
func isDirOnlyCommand(line string, pos int) bool {
	// Use pipe context so "ls | cd <TAB>" correctly identifies cd
	segment, segPos := ExtractPipeContext(line, pos)
	parts := strings.Fields(segment[:segPos])
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "cd", "pushd", "popd", "rmdir":
		return true
	}
	return false
}

// expandTilde expands ~ to home directory.
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~") {
		home := os.Getenv("HOME")
		if home != "" {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// getCompletionPrefix calculates the prefix to preserve.
func getCompletionPrefix(original, matched string) string {
	if matched == "" {
		return original
	}
	// Return the directory part of original
	dir := filepath.Dir(original)
	if dir == "." {
		// Preserve "./" if user explicitly typed it
		if strings.HasPrefix(original, "./") {
			return "./"
		}
		return ""
	}
	// filepath.Dir strips "./" prefix, so restore it if original had it
	if strings.HasPrefix(original, "./") && !strings.HasPrefix(dir, "./") {
		dir = "./" + dir
	}
	// Don't add trailing slash if dir already ends with one (root directory case)
	if strings.HasSuffix(dir, "/") {
		return dir
	}
	return dir + "/"
}

// fileDescription returns a short description for a file entry.
func fileDescription(entry os.DirEntry, isDir bool) string {
	if isDir {
		return "directory"
	}
	return fileTypeName(entry.Name())
}

// fileTypeName returns a human-readable file type from the extension.
func fileTypeName(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".md":
		return "markdown"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".txt":
		return "text"
	case ".toml":
		return "toml"
	case ".sum":
		return "checksum"
	case ".mod":
		return "go module"
	default:
		if ext != "" {
			return ext[1:] // strip dot
		}
		return "file"
	}
}

// getFileIcon returns a Nerd Font icon for the file type.
func getFileIcon(entry os.DirEntry) string {
	if entry.IsDir() {
		return ""
	}

	name := entry.Name()
	ext := strings.ToLower(filepath.Ext(name))

	switch ext {
	case ".go":
		return ""
	case ".py":
		return ""
	case ".js", ".ts":
		return ""
	case ".md":
		return ""
	case ".json":
		return ""
	case ".yaml", ".yml":
		return ""
	case ".sh", ".bash", ".zsh":
		return ""
	case ".txt":
		return ""
	case ".git":
		return ""
	default:
		return ""
	}
}
