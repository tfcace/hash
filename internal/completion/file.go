package completion

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/tilde"
	"github.com/tfcace/hash/internal/trace"
)

// FileCompleter completes filesystem paths.
type FileCompleter struct {
	showHidden bool
	fuzzyMode  bool
	readDir    func(string) ([]os.DirEntry, error)
	stat       func(string) (os.FileInfo, error) // a symlink's target
	getwd      func() string                     // the shell's working directory, for relative paths
	readPolicy flightPolicy
	reads      flightCache[[]os.DirEntry]
	links      flightCache[entryKind] // symlink targets' kinds, by link path
	ready      readyNotifier
}

// fileReadPolicy keeps a listing briefly so a burst of Tabs reads each
// directory once. A directory slower than the Tab budget (a network mount)
// reports pending and fills the menu in when its listing lands; after that,
// its last listing stands in while it is read again, but only when the
// reread is slow: a quick directory always answers with what is there now.
// Errors are not remembered: an unreadable directory is retried on the next
// Tab. Directory reads cannot be canceled, so a read stuck on a hung mount
// fails at the timeout and holds its directory's slot until it returns;
// meanwhile the last listing, if any, keeps standing in until its grace
// ends, and a few hung reads in one directory keep its other directories
// from launching more (inParentDirectory).
var fileReadPolicy = flightPolicy{
	ttl:        500 * time.Millisecond,
	staleGrace: 30 * time.Second,
	staleWaits: true,
	timeout:    time.Second,
	wait:       sourceWait,
}

// NewFileCompleter creates a new filesystem completer.
func NewFileCompleter() *FileCompleter {
	c := &FileCompleter{
		readDir:    os.ReadDir,
		stat:       os.Stat,
		getwd:      defaultGetwd,
		readPolicy: fileReadPolicy,
	}
	c.reads.ready = &c.ready
	c.reads.near = inParentDirectory
	c.links.near = inParentDirectory
	return c
}

// inParentDirectory reports whether path is in the directory that holds
// hung, at any depth: a read hung on a dead mount hangs its siblings, but
// says nothing about directories elsewhere.
func inParentDirectory(path, hung string) bool {
	parent := filepath.Dir(filepath.Clean(hung))
	path = filepath.Clean(path)
	return path == parent || strings.HasPrefix(path, strings.TrimSuffix(parent, string(filepath.Separator))+string(filepath.Separator))
}

// SetOnReady registers a callback fired whenever a directory listing lands,
// so a "fetching" menu for a slow directory can fill itself in.
func (c *FileCompleter) SetOnReady(fn func()) {
	c.ready.set(fn)
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

	cw := parseCommandWords(line, pos)
	word := splitWord(cw.current)
	dir, prefix := word.dir, word.name

	// Check if the command only accepts directories (cd, pushd, popd, rmdir)
	dirsOnly := isDirOnlyCommand(cw.command())
	if traceEnabled {
		trace.Emit("completion", "file_start", trace.LevelDetailed, map[string]any{
			"line":       line,
			"pos":        pos,
			"word":       cw.current,
			"dir":        dir,
			"prefix":     prefix,
			"dirs_only":  dirsOnly,
			"fuzzy_mode": c.fuzzyMode,
		})
	}

	readStart := time.Now()
	entries, status := c.readDirectory(ctx, dir)
	if traceEnabled {
		trace.Emit("completion", "file_readdir_done", trace.LevelDetailed, map[string]any{
			"dir":         dir,
			"entries":     len(entries),
			"status":      status.String(),
			"duration_ms": float64(time.Since(readStart).Microseconds()) / 1000.0,
		})
	}
	switch status {
	case lookupPending:
		// Still reading: say so, and the ready notification retries.
		return pendingResult(), nil
	case lookupFailed:
		// Unreadable (or hung past the timeout): nothing to offer.
		return Result{}, nil
	}

	// A typed leading dot asks for the dotfiles.
	showHidden := c.showHidden || strings.HasPrefix(prefix, ".")

	items := dotDirectories(prefix)
	var hiddenSkipped, prefixSkipped, nonDirSkipped, linkAssumed int
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

		value := name
		kind := kindOf(entry)
		// A link whose target did not answer (a dead mount, or broken)
		// might be a directory, for a command that takes only those.
		if dirsOnly && kind == unresolvedLink {
			kind = directory
			linkAssumed++
		}
		isDir := kind == directory
		if isDir {
			value += "/"
		}

		// Skip non-directories for directory-only commands
		if dirsOnly && !isDir {
			nonDirSkipped++
			continue
		}

		// Every match goes to the router, which keeps the best (fuzzy
		// mode) or the first, and says when it left more out.
		items = append(items, Item{
			Value:       value,
			Display:     name,
			Icon:        getFileIcon(entry),
			Description: fileDescription(entry, kind),
		})
	}

	if traceEnabled {
		trace.Emit("completion", "file_done", trace.LevelDetailed, map[string]any{
			"entries":          len(entries),
			"items":            len(items),
			"hidden_skipped":   hiddenSkipped,
			"prefix_skipped":   prefixSkipped,
			"non_dir_skipped":  nonDirSkipped,
			"symlink_assumed":  linkAssumed,
			"duration_ms":      float64(time.Since(start).Microseconds()) / 1000.0,
			"context_canceled": ctx.Err() != nil,
		})
	}

	return Result{
		Items:  items,
		Prefix: word.typed,
	}, nil
}

// readDirectory lists dir, sharing one read among concurrent completions.
// The returned slice is shared with the cache and must not be modified.
func (c *FileCompleter) readDirectory(ctx context.Context, dir string) ([]os.DirEntry, lookupStatus) {
	// Read the absolute path: the read runs detached, and by the time it
	// starts the shell may have cd'd elsewhere. It is also the cache key.
	path := dir
	if !filepath.IsAbs(path) {
		// Not Join, which would resolve lnk/.. by its text.
		path = strings.TrimSuffix(c.getwd(), "/") + "/" + dir
	}
	readDir, stat, links := c.readDir, c.stat, &c.links // the read runs detached
	return c.reads.get(ctx, path, c.readPolicy, func(context.Context) ([]os.DirEntry, error) {
		entries, err := readDir(path)
		if err != nil {
			return nil, err
		}
		return resolveLinks(path, entries, links, stat), nil
	})
}

// entryKind is what a listed name is, for completing it.
type entryKind uint8

const (
	plainFile      entryKind = iota // not a directory, or a link to something else
	directory                       // a directory, or a link to one
	unresolvedLink                  // a link whose target did not answer in time, or is missing
)

// linkEntry is a symlink whose target's kind its directory's read resolved.
type linkEntry struct {
	os.DirEntry
	kind entryKind
}

// kindOf returns what entry is: a link is what it points at, once its
// directory's read resolved it.
func kindOf(entry os.DirEntry) entryKind {
	switch {
	case entry.IsDir():
		return directory
	case entry.Type()&os.ModeSymlink == 0:
		return plainFile
	}
	if link, ok := entry.(linkEntry); ok {
		return link.kind
	}
	return unresolvedLink
}

// linkWait bounds how long a directory's read waits for its links'
// targets: a link into a dead mount must not hold the listing up.
const linkWait = 50 * time.Millisecond

// linkPolicy states a symlink's target once per listing that asks. A
// target still unanswered at linkWait is hung: its stat keeps the link's
// slot until it returns, so later listings report the link unresolved at
// once instead of blocking another goroutine on the same dead mount, and a
// few hung links in a directory keep its other links from being stated
// (inParentDirectory).
var linkPolicy = flightPolicy{timeout: linkWait, wait: waitUntilCanceled}

// resolveLinks returns entries, read from dir, with each symlink a
// linkEntry saying what it points at. Targets are stated one after another
// through links, on a goroutine of their own, for at most linkWait in all:
// a link still unanswered then stays unresolved.
func resolveLinks(
	dir string,
	entries []os.DirEntry,
	links *flightCache[entryKind],
	stat func(string) (os.FileInfo, error),
) []os.DirEntry {
	var at []int
	for i, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			at = append(at, i)
		}
	}
	if len(at) == 0 {
		return entries
	}
	entries = slices.Clone(entries)
	type resolved struct {
		at   int
		kind entryKind
	}
	paths := make([]string, len(at))
	for k, i := range at {
		paths[k] = strings.TrimSuffix(dir, "/") + "/" + entries[i].Name()
	}
	answers := make(chan resolved, len(at))
	go func() {
		for k, path := range paths {
			kind, status := links.get(context.Background(), path, linkPolicy, func(context.Context) (entryKind, error) {
				info, err := stat(path)
				switch {
				case err != nil:
					return unresolvedLink, err
				case info.IsDir():
					return directory, nil
				}
				return plainFile, nil
			})
			if status != lookupFresh {
				kind = unresolvedLink
			}
			answers <- resolved{at[k], kind}
		}
	}()
	timeout := time.NewTimer(linkWait)
	defer timeout.Stop()
	for range at {
		select {
		case r := <-answers:
			entries[r.at] = linkEntry{entries[r.at], r.kind}
		case <-timeout.C:
			return entries
		}
	}
	return entries
}

// isDirOnlyCommand reports whether the command only accepts directories.
func isDirOnlyCommand(command string) bool {
	switch command {
	case "cd", "pushd", "popd", "rmdir":
		return true
	}
	return false
}

// expandTilde expands a leading ~/ or ~name/ (tildePrefix) as the shell
// does: to the home directory, or name's. Anything else is a path as it is.
func expandTilde(path string) string {
	if tildePrefix(path) == "" {
		return path
	}
	return tilde.Expand(path)
}

// tildePrefix returns the ~/ or ~name/ that path starts with, where name
// is one a user can have, or "": only that expands. ~'x;id'/ (the word
// ~x;id/) names a directory called "~x;id".
func tildePrefix(path string) string {
	name, _, ok := strings.Cut(strings.TrimPrefix(path, "~"), "/")
	if !ok || !strings.HasPrefix(path, "~") || !isLoginName(name) {
		return ""
	}
	return "~" + name + "/"
}

// isLoginName reports whether name, after a ~, can be a user's: empty (the
// user's own home) or portable filename characters, not starting with -.
func isLoginName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !isEnvChar(c) && c != '.' && (c != '-' || i == 0) {
			return false
		}
	}
	return true
}

// pathWord is a path being completed, split after its last /.
type pathWord struct {
	typed string // the directory as typed, the text before each item ("" for none)
	dir   string // the directory to list: typed, its ~ expanded ("." for none)
	name  string // the name to match
}

// splitWord splits the word being completed after its last /. The
// directory goes to the kernel as typed, which resolves a .. after a
// symlink (lnk/..) to the target's parent, as the command will.
func splitWord(word string) pathWord {
	slash := strings.LastIndexByte(word, '/')
	w := pathWord{typed: word[:slash+1], name: word[slash+1:]}
	w.dir = expandTilde(w.typed)
	if w.dir == "" {
		w.dir = "."
	}
	return w
}

// dotDirectories returns the items for . and .. that name, typed, starts:
// a directory listing leaves them out, and bash offers them for a typed dot.
func dotDirectories(name string) []Item {
	var items []Item
	for _, dot := range []string{".", ".."} {
		if name != "" && strings.HasPrefix(dot, name) {
			items = append(items, Item{Value: dot + "/", Display: dot, Description: "directory"})
		}
	}
	return items
}

// fileDescription returns a short description for a file entry.
func fileDescription(entry os.DirEntry, kind entryKind) string {
	switch kind {
	case directory:
		return "directory"
	case unresolvedLink:
		return "symlink"
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
