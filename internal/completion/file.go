package completion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// FileCompleter completes filesystem paths.
type FileCompleter struct {
	showHidden bool
	fuzzyMode  bool
}

// NewFileCompleter creates a new filesystem completer.
func NewFileCompleter() *FileCompleter {
	return &FileCompleter{
		showHidden: false,
		fuzzyMode:  false,
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
func (c *FileCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	// Extract the word being completed
	word := extractWord(line, pos)
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

	// Read directory
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Result{}, nil //nolint:nilerr // graceful degradation: return empty on unreadable dir
	}

	// Show hidden files if user is explicitly typing a dot prefix
	// Use originalWord to avoid false positive when word defaults to "."
	wantsDotFiles := originalWord != "" && strings.HasPrefix(filepath.Base(originalWord), ".")
	showHidden := c.showHidden || wantsDotFiles

	var items []Item
	for _, entry := range entries {
		name := entry.Name()

		// Skip hidden files unless enabled or prefix starts with "."
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}

		// Skip if doesn't match prefix (unless fuzzy mode - let router filter)
		if !c.fuzzyMode && prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			continue
		}

		// Build completion value
		value := name
		isDir := entry.IsDir()
		// For symlinks, check if the target is a directory
		if entry.Type()&os.ModeSymlink != 0 {
			if target, err := os.Stat(filepath.Join(dir, name)); err == nil {
				isDir = target.IsDir()
			}
		}
		if isDir {
			value += "/"
		}

		items = append(items, Item{
			Value:   value,
			Display: name,
			Icon:    getFileIcon(entry),
		})
	}

	return Result{
		Items:  items,
		Prefix: getCompletionPrefix(word, prefix),
	}, nil
}

// extractWord extracts the word at position from the line.
func extractWord(line string, pos int) string {
	if pos > len(line) {
		pos = len(line)
	}

	// Find start of word (go backwards until space or start)
	start := pos
	for start > 0 && line[start-1] != ' ' && line[start-1] != '\t' {
		start--
	}

	return line[start:pos]
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
