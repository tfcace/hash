package completion

import (
	"context"
	"os"
	"regexp"
	"strings"
)

// MakeHandler provides completions for make targets.
type MakeHandler struct {
	readFile func(path string) ([]string, error)
}

// NewMakeHandler creates a make completion handler.
func NewMakeHandler() *MakeHandler {
	return &MakeHandler{readFile: readLines}
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

	targets := h.parseTargets()
	return prefixFilterItems(targets, current)
}

func (h *MakeHandler) parseTargets() []string {
	// Try Makefile, then GNUmakefile, then makefile
	for _, name := range []string{"Makefile", "GNUmakefile", "makefile"} {
		if _, err := os.Stat(name); err == nil {
			return h.parseFile(name)
		}
	}
	return nil
}

func (h *MakeHandler) parseFile(path string) []string {
	lines, err := h.readFile(path)
	if err != nil {
		return nil
	}

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
