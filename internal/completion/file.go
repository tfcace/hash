package completion

import (
	"context"
	"fmt"
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
//
//nolint:gocyclo // file completion handles multiple path formats and edge cases
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
			Value:       value,
			Display:     name,
			Icon:        getFileIcon(entry),
			Description: fileDescription(filepath.Join(dir, name), entry, isDir),
		})
	}

	return Result{
		Items:  items,
		Prefix: getCompletionPrefix(originalWord, prefix),
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

// fileDescription returns a short description for a file entry (type + size).
func fileDescription(path string, entry os.DirEntry, isDir bool) string {
	if isDir {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "directory"
		}
		n := 0
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				n++
			}
		}
		if n == 1 {
			return "1 item"
		}
		return fmt.Sprintf("%d items", n)
	}

	info, err := entry.Info()
	if err != nil {
		return fileTypeName(entry.Name())
	}
	return fileTypeName(entry.Name()) + "  " + formatSize(info.Size())
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

// formatSize returns a human-readable file size.
func formatSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
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
