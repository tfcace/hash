package completion

import (
	"context"
	"strings"
	"time"
)

// manAproposTimeout bounds one apropos query; on macOS a cold whatis
// database can take a second.
const manAproposTimeout = 2 * time.Second

// ManHandler provides completions for the man command.
type ManHandler struct {
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)
	cacheTTL   time.Duration
	pages      flightCache[[]string] // apropos output keyed by query
}

// NewManHandler creates a man completion handler.
func NewManHandler() *ManHandler {
	return &ManHandler{runCommand: runIsolatedCommand, cacheTTL: time.Minute}
}

func (h *ManHandler) setReady(n *readyNotifier) { h.pages.ready = n }

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

	// apropos exits non-zero when nothing matches: that failure is
	// remembered like the answer would be.
	policy := flightPolicy{ttl: h.cacheTTL, failureTTL: h.cacheTTL, timeout: manAproposTimeout, wait: sourceWait}
	lines, status := h.pages.get(ctx, current, policy, func(ctx context.Context) ([]string, error) {
		run := h.runCommand
		if run == nil {
			run = runIsolatedCommand
		}
		return run(ctx, "apropos", current)
	})
	if status == lookupPending || status == lookupFailed {
		return sourceResult(nil, status, current)
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
