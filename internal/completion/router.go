package completion

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tfcace/hash/internal/trace"
)

// Router dispatches completion requests to registered completers.
type Router struct {
	completers        []registeredCompleter
	nextCompleterID   uint64
	fuzzy             bool
	completerMu       sync.Mutex
	completerInFlight map[uint64]bool
	prefetchMu        sync.Mutex
	prefetchInFlight  map[uint64]bool
}

type registeredCompleter struct {
	completer Completer
	priority  Priority
	id        uint64
}

type boundedCompletionResult struct {
	result Result
	err    error
}

type completerCallResult struct {
	result Result
	err    error
}

// NewRouter creates a new completion router.
func NewRouter() *Router {
	return &Router{}
}

// SetFuzzy enables or disables fuzzy filtering of results.
func (r *Router) SetFuzzy(enabled bool) {
	r.fuzzy = enabled
}

// Fuzzy returns whether fuzzy filtering is enabled.
func (r *Router) Fuzzy() bool {
	return r.fuzzy
}

// Register adds a completer with the given priority.
// Lower priority values are tried first.
func (r *Router) Register(c Completer, priority Priority) {
	r.nextCompleterID++
	r.completers = append(r.completers, registeredCompleter{
		completer: c,
		priority:  priority,
		id:        r.nextCompleterID,
	})

	// Sort by priority (lower first)
	sort.Slice(r.completers, func(i, j int) bool {
		if r.completers[i].priority != r.completers[j].priority {
			return r.completers[i].priority < r.completers[j].priority
		}
		return r.completers[i].id < r.completers[j].id
	})
}

// Complete tries each completer in priority order until one returns results.
func (r *Router) Complete(ctx context.Context, line string, pos int) (Result, error) {
	start := time.Now()
	traceEnabled := trace.Enabled("completion")

	// Extract the query (word being completed) for fuzzy filtering
	query := extractCompletionQuery(line, pos)
	if traceEnabled {
		trace.Emit("completion", "router_start", trace.LevelDetailed, map[string]any{
			"line":       line,
			"pos":        pos,
			"query":      query,
			"fuzzy":      r.fuzzy,
			"completers": len(r.completers),
		})
	}

	for _, rc := range r.completers {
		select {
		case <-ctx.Done():
			if traceEnabled {
				trace.Emit("completion", "router_canceled", trace.LevelDetailed, map[string]any{
					"duration_ms": float64(time.Since(start).Microseconds()) / 1000.0,
				})
			}
			return Result{}, nil
		default:
		}

		completerStart := time.Now()
		if traceEnabled {
			trace.Emit("completion", "completer_start", trace.LevelDetailed, map[string]any{
				"name":     rc.completer.Name(),
				"priority": int(rc.priority),
			})
		}

		result, err := r.completeWithBoundary(ctx, rc, line, pos)
		if traceEnabled {
			errText := ""
			if err != nil {
				errText = err.Error()
			}
			trace.Emit("completion", "completer_done", trace.LevelDetailed, map[string]any{
				"name":        rc.completer.Name(),
				"priority":    int(rc.priority),
				"items":       len(result.Items),
				"error":       errText,
				"duration_ms": float64(time.Since(completerStart).Microseconds()) / 1000.0,
			})
		}
		if err != nil {
			continue // Try next completer on error
		}

		if len(result.Items) > 0 {
			// Apply fuzzy filtering if enabled
			// Skip if query ends with "/" - we're listing directory contents, not filtering
			if r.fuzzy && query != "" && !strings.HasSuffix(query, "/") {
				// For paths like "~/Go", only filter on the basename "Go"
				// The prefix (directory path) is handled by the completer
				filterQuery := query
				if lastSlash := strings.LastIndex(query, "/"); lastSlash >= 0 {
					filterQuery = query[lastSlash+1:]
				}
				if filterQuery != "" {
					result.Items = FuzzyFilter(result.Items, filterQuery)
				}
			}
			result.Items = limitCompletionItems(result.Items)
			if traceEnabled {
				trace.Emit("completion", "router_done", trace.LevelDetailed, map[string]any{
					"winner":      rc.completer.Name(),
					"items":       len(result.Items),
					"duration_ms": float64(time.Since(start).Microseconds()) / 1000.0,
				})
			}
			return result, nil
		}
	}

	if traceEnabled {
		trace.Emit("completion", "router_done", trace.LevelDetailed, map[string]any{
			"winner":      "",
			"items":       0,
			"duration_ms": float64(time.Since(start).Microseconds()) / 1000.0,
		})
	}
	return Result{}, nil
}

func (r *Router) completeWithBoundary(ctx context.Context, rc registeredCompleter, line string, pos int) (Result, error) {
	key := completerInFlightKey(rc)
	if !r.beginCompleterCall(key) {
		return Result{}, nil
	}

	done := make(chan completerCallResult, 1)
	go func() {
		defer r.endCompleterCall(key)
		result, err := rc.completer.Complete(ctx, line, pos)
		done <- completerCallResult{result: result, err: err}
	}()

	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case result := <-done:
		return result.result, result.err
	}
}

func completerInFlightKey(rc registeredCompleter) uint64 {
	return rc.id
}

func (r *Router) beginCompleterCall(key uint64) bool {
	r.completerMu.Lock()
	defer r.completerMu.Unlock()
	if r.completerInFlight == nil {
		r.completerInFlight = make(map[uint64]bool)
	}
	if r.completerInFlight[key] {
		return false
	}
	r.completerInFlight[key] = true
	return true
}

func (r *Router) endCompleterCall(key uint64) {
	r.completerMu.Lock()
	delete(r.completerInFlight, key)
	r.completerMu.Unlock()
}

// CompleteBounded runs completion behind the provided context boundary.
// This protects UI callers from completers that ignore context cancellation.
func (r *Router) CompleteBounded(ctx context.Context, line string, pos int) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	done := make(chan boundedCompletionResult, 1)
	go func() {
		result, err := r.Complete(ctx, line, pos)
		done <- boundedCompletionResult{result: result, err: err}
	}()

	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case result := <-done:
		return result.result, result.err
	}
}

// extractCompletionQuery extracts the word being completed.
func extractCompletionQuery(line string, pos int) string {
	return shellUnescapeWord(shellWordAt(line, pos))
}

// ExtractPipeContext extracts the command context after the last pipe.
// For "cat file | pb", returns ("pb", 3) where 3 is the new position.
// For "ls -la", returns ("ls -la", 5) unchanged.
// This allows completers to work correctly with piped commands.
func ExtractPipeContext(line string, pos int) (extracted string, newPos int) {
	if pos > len(line) {
		pos = len(line)
	}

	// Find the last pipe character before pos
	lastPipe := -1
	for i := pos - 1; i >= 0; i-- {
		if line[i] == '|' {
			lastPipe = i
			break
		}
	}

	if lastPipe < 0 {
		// No pipe, return original
		return line, pos
	}

	// Skip the pipe and any leading whitespace
	start := lastPipe + 1
	for start < pos && (line[start] == ' ' || line[start] == '\t') {
		start++
	}

	// Return the segment after the pipe with adjusted position
	extracted = line[start:pos]
	newPos = pos - start
	return extracted, newPos
}

// Completers returns the registered completers for inspection.
func (r *Router) Completers() []Completer {
	result := make([]Completer, len(r.completers))
	for i, rc := range r.completers {
		result[i] = rc.completer
	}
	return result
}

// Prefetcher is an optional interface for completers that support background prefetching.
type Prefetcher interface {
	Prefetch(line string, pos int)
}

// Prefetch triggers background prefetching for all completers that support it.
// Call this when the user types a space after a command.
func (r *Router) Prefetch(line string, pos int) {
	for _, rc := range r.completers {
		if p, ok := rc.completer.(Prefetcher); ok {
			p.Prefetch(line, pos)
		}
	}
}

// PrefetchBounded runs prefetch work in the background and coalesces stuck prefetchers.
func (r *Router) PrefetchBounded(line string, pos int) {
	for _, rc := range r.completers {
		p, ok := rc.completer.(Prefetcher)
		if !ok {
			continue
		}
		if !r.beginPrefetch(rc.id) {
			continue
		}
		go func(id uint64, prefetcher Prefetcher) {
			defer r.endPrefetch(id)
			prefetcher.Prefetch(line, pos)
		}(rc.id, p)
	}
}

func (r *Router) beginPrefetch(key uint64) bool {
	r.prefetchMu.Lock()
	defer r.prefetchMu.Unlock()
	if r.prefetchInFlight == nil {
		r.prefetchInFlight = make(map[uint64]bool)
	}
	if r.prefetchInFlight[key] {
		return false
	}
	r.prefetchInFlight[key] = true
	return true
}

func (r *Router) endPrefetch(key uint64) {
	r.prefetchMu.Lock()
	delete(r.prefetchInFlight, key)
	r.prefetchMu.Unlock()
}
