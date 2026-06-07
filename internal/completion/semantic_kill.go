package completion

import (
	"context"
	"strings"
	"sync"
	"time"
)

// KillHandler provides completions for kill and killall commands.
type KillHandler struct {
	command       string
	listProcesses func(ctx context.Context) ([]processInfo, error)
	cacheMu       sync.Mutex
	cacheTTL      time.Duration
	cacheExpires  time.Time
	cached        []processInfo
	now           func() time.Time

	inflightMu         sync.Mutex
	inflight           map[string]*processListCall
	inflightMaxWaitAge time.Duration
}

type processInfo struct {
	PID  string
	Name string
}

type processListCall struct {
	done      chan struct{}
	started   time.Time
	processes []processInfo
	err       error
}

// NewKillHandler creates a kill/killall completion handler.
func NewKillHandler(command string) *KillHandler {
	return &KillHandler{
		command:       command,
		listProcesses: defaultListProcesses,
		cacheTTL:      time.Second,
		now:           time.Now,
	}
}

// Commands returns the commands this handler supports.
func (h *KillHandler) Commands() []string {
	if h.command == "" {
		return []string{"kill"}
	}
	return []string{h.command}
}

// Complete returns process completions.
func (h *KillHandler) Complete(ctx context.Context, args []string, current string) Result {
	if strings.HasPrefix(current, "-") {
		return Result{}
	}

	processes, err := h.cachedProcesses(ctx)
	if err != nil {
		return Result{}
	}

	var items []Item
	seen := make(map[string]bool)
	for _, p := range processes {
		if h.command == "killall" {
			if !seen[p.Name] && strings.HasPrefix(p.Name, current) {
				seen[p.Name] = true
				items = append(items, Item{
					Value:   p.Name,
					Display: p.Name,
				})
			}
			continue
		}

		if !seen[p.PID] && strings.HasPrefix(p.PID, current) {
			seen[p.PID] = true
			items = append(items, Item{
				Value:       p.PID,
				Display:     p.PID,
				Description: p.Name,
			})
		}
	}
	return Result{Items: items}
}

func (h *KillHandler) cachedProcesses(ctx context.Context) ([]processInfo, error) {
	if h.cacheTTL > 0 {
		h.cacheMu.Lock()
		if h.timeNow().Before(h.cacheExpires) {
			processes := append([]processInfo(nil), h.cached...)
			h.cacheMu.Unlock()
			return processes, nil
		}
		h.cacheMu.Unlock()
	}

	processes, err := h.lookupProcesses(ctx)
	if err != nil {
		return nil, err
	}
	if h.cacheTTL > 0 {
		h.cacheMu.Lock()
		h.cached = append([]processInfo(nil), processes...)
		h.cacheExpires = h.timeNow().Add(h.cacheTTL)
		h.cacheMu.Unlock()
	}
	return processes, nil
}

func (h *KillHandler) lookupProcesses(ctx context.Context) ([]processInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	key := h.command
	now := time.Now()
	h.inflightMu.Lock()
	if call, ok := h.inflight[key]; ok {
		if !contextReadCallIsStale(call.started, now, h.inflightMaxWaitAge) {
			h.inflightMu.Unlock()
			return waitForProcessList(ctx, call)
		}
		delete(h.inflight, key)
	}

	call := &processListCall{done: make(chan struct{}), started: now}
	if h.inflight == nil {
		h.inflight = make(map[string]*processListCall)
	}
	h.inflight[key] = call
	h.inflightMu.Unlock()

	go h.finishProcessList(ctx, key, call)
	return waitForProcessList(ctx, call)
}

func (h *KillHandler) finishProcessList(ctx context.Context, key string, call *processListCall) {
	list := h.listProcesses
	if list == nil {
		list = defaultListProcesses
	}
	processes, err := list(ctx)

	h.inflightMu.Lock()
	call.processes = processes
	call.err = err
	if h.inflight[key] == call {
		delete(h.inflight, key)
	}
	h.inflightMu.Unlock()
	close(call.done)
}

func waitForProcessList(ctx context.Context, call *processListCall) ([]processInfo, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return append([]processInfo(nil), call.processes...), call.err
	}
}

func (h *KillHandler) timeNow() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func defaultListProcesses(ctx context.Context) ([]processInfo, error) {
	queryCtx, cancel := context.WithTimeout(ctx, vcsQueryTimeout)
	defer cancel()

	lines, err := runIsolatedCommand(queryCtx, "ps", "-eo", "pid,comm")
	if err != nil {
		return nil, err
	}

	var processes []processInfo
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid := fields[0]
		// Skip header
		if pid == "PID" {
			continue
		}
		// Extract just the binary name from the path
		name := fields[1]
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		// Strip leading "-" from login shells
		name = strings.TrimPrefix(name, "-")
		if name == "" {
			continue
		}
		processes = append(processes, processInfo{PID: pid, Name: name})
	}
	return processes, nil
}
