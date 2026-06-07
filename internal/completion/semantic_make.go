package completion

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"
)

// MakeHandler provides completions for make targets.
type MakeHandler struct {
	readFile func(path string) ([]string, error)
	reader   contextLinesReader
	cache    stringListCache
	cacheTTL time.Duration
	now      func() time.Time
}

// NewMakeHandler creates a make completion handler.
func NewMakeHandler() *MakeHandler {
	return &MakeHandler{
		readFile: readLines,
		cacheTTL: 2 * time.Second,
		now:      time.Now,
	}
}

// Commands returns the commands this handler supports.
func (h *MakeHandler) Commands() []string {
	return []string{"make"}
}

var makeTargetRe = regexp.MustCompile(`^([a-zA-Z0-9_][a-zA-Z0-9_.-]*)\s*:`)

// Complete returns Makefile target completions.
func (h *MakeHandler) Complete(ctx context.Context, args []string, current string) Result {
	if strings.HasPrefix(current, "-") {
		return Result{}
	}

	targets := h.parseTargets(ctx)
	return prefixFilterItems(targets, current)
}

func (h *MakeHandler) parseTargets(ctx context.Context) []string {
	cacheKey := h.cacheKey()
	if h.cacheTTL > 0 {
		if targets, ok := h.cache.get(cacheKey, h.timeNow()); ok {
			return targets
		}
	}

	// Try Makefile, then GNUmakefile, then makefile
	for _, name := range []string{"Makefile", "GNUmakefile", "makefile"} {
		targets, ok := h.parseExistingFile(ctx, name)
		if !ok {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		if h.cacheTTL > 0 {
			h.cache.set(cacheKey, targets, h.timeNow().Add(h.cacheTTL))
		}
		return targets
	}
	if ctx.Err() != nil {
		return nil
	}
	if h.cacheTTL > 0 {
		h.cache.set(cacheKey, nil, h.timeNow().Add(h.cacheTTL))
	}
	return nil
}

func (h *MakeHandler) parseExistingFile(ctx context.Context, path string) ([]string, bool) {
	lines, err := h.reader.read(ctx, h.readFile, path)
	if err != nil {
		return nil, false
	}
	return h.parseLines(lines), true
}

func (h *MakeHandler) cacheKey() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

func (h *MakeHandler) timeNow() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func (h *MakeHandler) parseFile(path string) []string {
	lines, err := h.readFile(path)
	if err != nil {
		return nil
	}
	return h.parseLines(lines)
}

func (h *MakeHandler) parseLines(lines []string) []string {
	seen := make(map[string]bool)
	var targets []string
	for _, line := range lines {
		matches := makeTargetRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		target := matches[1]
		// Skip special targets
		if strings.HasPrefix(target, ".") {
			continue
		}
		if !seen[target] {
			seen[target] = true
			targets = append(targets, target)
		}
	}
	return targets
}
