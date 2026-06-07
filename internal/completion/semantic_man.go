package completion

import (
	"context"
	"strings"
	"sync"
	"time"
)

// ManHandler provides completions for the man command.
type ManHandler struct {
	runCommand func(ctx context.Context, name string, args ...string) ([]string, error)

	inflightMu         sync.Mutex
	inflight           map[string]*manAproposCall
	inflightMaxWaitAge time.Duration
}

type manAproposCall struct {
	done    chan struct{}
	started time.Time
	lines   []string
	err     error
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

	lines, err := h.lookupApropos(queryCtx, current)
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

func (h *ManHandler) lookupApropos(ctx context.Context, current string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	now := time.Now()
	h.inflightMu.Lock()
	if call, ok := h.inflight[current]; ok {
		if !contextReadCallIsStale(call.started, now, h.inflightMaxWaitAge) {
			h.inflightMu.Unlock()
			return waitForManApropos(ctx, call)
		}
		delete(h.inflight, current)
	}

	call := &manAproposCall{done: make(chan struct{}), started: now}
	if h.inflight == nil {
		h.inflight = make(map[string]*manAproposCall)
	}
	h.inflight[current] = call
	h.inflightMu.Unlock()

	go h.finishApropos(ctx, current, call)
	return waitForManApropos(ctx, call)
}

func (h *ManHandler) finishApropos(ctx context.Context, current string, call *manAproposCall) {
	run := h.runCommand
	if run == nil {
		run = runIsolatedCommand
	}
	lines, err := run(ctx, "apropos", current)

	h.inflightMu.Lock()
	call.lines = lines
	call.err = err
	if h.inflight[current] == call {
		delete(h.inflight, current)
	}
	h.inflightMu.Unlock()
	close(call.done)
}

func waitForManApropos(ctx context.Context, call *manAproposCall) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return append([]string(nil), call.lines...), call.err
	}
}
