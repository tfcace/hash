package completion

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MakeHandler provides completions for make targets.
type MakeHandler struct {
	readFile func(path string) ([]string, error)
	cacheTTL time.Duration
	targets  flightCache[[]string] // keyed by directory
}

// NewMakeHandler creates a make completion handler.
func NewMakeHandler() *MakeHandler {
	return &MakeHandler{
		readFile: readLines,
		cacheTTL: 2 * time.Second,
	}
}

func (h *MakeHandler) setReady(n *readyNotifier) { h.targets.ready = n }

var errNoMakefile = errors.New("completion: no makefile")

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

	targets, status := h.parseTargets(ctx)
	return sourceResult(targets, status, current)
}

// parseTargets returns the targets of the makefile in the working directory.
// A missing makefile is remembered like any other failure, for cacheTTL.
func (h *MakeHandler) parseTargets(ctx context.Context) ([]string, lookupStatus) {
	dir := defaultGetwd()
	policy := flightPolicy{
		ttl:        h.cacheTTL,
		failureTTL: h.cacheTTL,
		timeout:    fileSourceTimeout,
		wait:       sourceWait,
	}
	return h.targets.get(ctx, dir, policy, func(context.Context) ([]string, error) {
		// Try Makefile, then GNUmakefile, then makefile
		for _, name := range []string{"Makefile", "GNUmakefile", "makefile"} {
			if lines, err := h.readFile(filepath.Join(dir, name)); err == nil {
				return h.parseLines(lines), nil
			}
		}
		return nil, errNoMakefile
	})
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
