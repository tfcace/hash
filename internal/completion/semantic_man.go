package completion

import (
	"context"
	"strings"
)

// ManHandler provides completions for the man command.
type ManHandler struct {
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)
}

// NewManHandler creates a man completion handler.
func NewManHandler() *ManHandler {
	return &ManHandler{runCommand: runIsolatedCommand}
}

// Commands returns the commands this handler supports.
func (h *ManHandler) Commands() []string {
	return []string{"man"}
}

// Complete returns man page completions using apropos.
func (h *ManHandler) Complete(ctx context.Context, args []string, current string) Result {
	if strings.HasPrefix(current, "-") {
		return Result{}
	}
	if current == "" {
		return Result{}
	}

	queryCtx, cancel := context.WithTimeout(ctx, 200*vcsQueryTimeout/150) // ~200ms
	defer cancel()

	lines, err := h.runCommand(queryCtx, "apropos", current)
	if err != nil {
		return Result{}
	}

	var items []Item
	seen := make(map[string]bool)
	for _, line := range lines {
		// apropos output format: "name (section) - description"
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 0 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" || seen[name] {
			continue
		}
		if !strings.HasPrefix(name, current) {
			continue
		}
		seen[name] = true

		desc := ""
		if dashIdx := strings.Index(line, " - "); dashIdx >= 0 {
			desc = strings.TrimSpace(line[dashIdx+3:])
		}
		items = append(items, Item{
			Value:       name,
			Display:     name,
			Description: desc,
		})
		if len(items) >= 20 {
			break
		}
	}
	return Result{Items: items}
}
