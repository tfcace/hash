package completion

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tfcace/hash/internal/shlex"
	"github.com/tfcace/hash/internal/trace"
	"mvdan.cc/sh/v3/syntax"
)

// Router dispatches completion requests to registered completers.
type Router struct {
	completers      []registeredCompleter
	nextCompleterID uint64
	fuzzy           bool
	// lang is the dialect hash parses a line in, which each completed line
	// is read back in: bash until SetDialect says otherwise.
	lang syntax.LangVariant
	// ready is the one "background data landed" callback. Every completer
	// that loads in the background forwards to it (see Register), so
	// SetOnReady can change it at any time and reaches them all.
	ready readyNotifier

	// calls are the completers with a Complete call running, prefetching
	// those with a Prefetch running. A busy one is skipped, never launched
	// again until its work returns, so goroutines cannot pile up behind one
	// that ignores its context.
	calls       busySet
	prefetching busySet
}

type registeredCompleter struct {
	completer Completer
	priority  Priority
	id        uint64
}

// busySet is the completers (by registration id) with work running.
type busySet struct {
	mu  sync.Mutex
	ids map[uint64]bool
}

// take marks id busy, reporting false when it already was.
func (b *busySet) take(id uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ids[id] {
		return false
	}
	if b.ids == nil {
		b.ids = make(map[uint64]bool)
	}
	b.ids[id] = true
	return true
}

func (b *busySet) release(id uint64) {
	b.mu.Lock()
	delete(b.ids, id)
	b.mu.Unlock()
}

// NewRouter creates a new completion router.
func NewRouter() *Router {
	return &Router{lang: syntax.LangBash}
}

// SetFuzzy enables or disables fuzzy filtering of results.
func (r *Router) SetFuzzy(enabled bool) {
	r.fuzzy = enabled
}

// SetDialect sets the shell dialect ("bash" or "zsh") hash's executor
// parses a line in, which each completed line is read back in before it is
// offered.
func (r *Router) SetDialect(dialect string) error {
	var lang syntax.LangVariant
	if err := lang.Set(dialect); err != nil {
		return err
	}
	if lang != syntax.LangBash && lang != syntax.LangZsh {
		return fmt.Errorf("unsupported shell dialect %q (supported: bash, zsh)", dialect)
	}
	r.lang = lang
	return nil
}

// Fuzzy returns whether fuzzy filtering is enabled.
func (r *Router) Fuzzy() bool {
	return r.fuzzy
}

// readyNotifiable is implemented by completers whose data loads in the
// background and that can therefore report OutcomePending.
type readyNotifiable interface {
	SetOnReady(fn func())
}

// SetOnReady registers a callback fired whenever any completer's background
// data lands, success or failure, so a UI showing "fetching" can retry
// without another keystroke. It applies to completers registered before
// and after the call.
func (r *Router) SetOnReady(fn func()) {
	r.ready.set(fn)
}

// Register adds a completer with the given priority.
// Lower priority values are tried first. A completer that loads data in the
// background is wired to the router's ready callback; calling its own
// SetOnReady afterwards would unwire it.
func (r *Router) Register(c Completer, priority Priority) {
	if n, ok := c.(readyNotifiable); ok {
		n.SetOnReady(r.ready.notify)
	}
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

	pos = clampCursor(line, pos)
	word := parseCommandWords(line, pos)
	query := word.current
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
		if ctx.Err() != nil {
			if traceEnabled {
				trace.Emit("completion", "router_canceled", trace.LevelDetailed, map[string]any{
					"duration_ms": float64(time.Since(start).Microseconds()) / 1000.0,
				})
			}
			return Result{}, nil
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

		switch result.Outcome() {
		case OutcomeNotMine:
			continue
		case OutcomeAnswer:
			if result, err = r.finalizeResult(ctx, result, word, line, pos); err != nil {
				return Result{}, err // the deadline passed before any item was checked
			}
		}
		if traceEnabled {
			event := "router_done"
			if result.Outcome() == OutcomePending {
				event = "router_pending"
			}
			trace.Emit("completion", event, trace.LevelDetailed, map[string]any{
				"winner":      rc.completer.Name(),
				"outcome":     result.Outcome().String(),
				"items":       len(result.Items),
				"duration_ms": float64(time.Since(start).Microseconds()) / 1000.0,
			})
		}
		return result, nil
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

// finalizeResult settles an answer for word, the word being completed at
// pos: where its replacement starts, which items fuzzy mode keeps, and the
// shell text each one inserts, checked until ctx's deadline.
func (r *Router) finalizeResult(ctx context.Context, result Result, word commandWords, line string, pos int) (Result, error) {
	query := word.current
	result.ReplaceFrom = min(result.ReplaceFrom, pos) // a completer bug; the slice below must not panic
	if result.ReplaceFrom <= word.currentStart {
		result.ReplaceFrom = word.currentStart
	} else {
		// The items complete only the end of the word ("$HO" of "$((1 + $HO").
		query = shlex.Unquote(line[result.ReplaceFrom:pos])
	}
	var filterCut, limitCut int
	if r.fuzzy && query != "" && !strings.HasSuffix(query, "/") {
		if filterQuery := itemQuery(query, result.Prefix); filterQuery != "" {
			result.Items, filterCut = fuzzyFilter(result.Items, filterQuery)
		}
	}
	result.Items, limitCut = limitCompletionItems(result.Items)
	result.hidden.More += filterCut + limitCut
	// Every candidate filtered out is still this completer's answer.
	result.empty = OutcomeNoMatches
	return writeShellText(ctx, result, line, pos, r.lang)
}

// itemQuery returns the part of the replaced text that the items themselves
// complete: the text minus the result's shared prefix (a directory), or
// else its last path element.
func itemQuery(query, prefix string) string {
	if prefix != "" && strings.HasPrefix(query, prefix) {
		return query[len(prefix):]
	}
	if lastSlash := strings.LastIndex(query, "/"); lastSlash >= 0 {
		return query[lastSlash+1:]
	}
	return query
}

// completeWithBoundary runs one completer behind the caller's context: the
// call runs on its own goroutine, so a completer that ignores its context
// cannot hold the caller past its deadline, and a panic in it is an error,
// not the end of the user's login shell.
//
// A completer whose last call is still running (its caller gave up, and it
// has not noticed) is skipped, not reported pending: nothing says it owns
// this input, no ready notification would follow, and launching another
// call would pile goroutines up behind it. Every built-in completer honors
// its context and every source times out (flightCache), so a call outlives
// its caller only by the moment it takes to notice, or for a buggy
// completer. The editor calls synchronously, so no two lookups wait on one
// call.
func (r *Router) completeWithBoundary(ctx context.Context, rc registeredCompleter, line string, pos int) (Result, error) {
	if !r.calls.take(rc.id) {
		return Result{}, nil
	}
	type answer struct {
		result Result
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		result, err := completeGuarded(ctx, rc.completer, line, pos)
		// Free the slot before answering: the caller's next lookup may come
		// right after it receives this one.
		r.calls.release(rc.id)
		done <- answer{result, err}
	}()
	select {
	case a := <-done:
		return a.result, a.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// completeGuarded runs one Complete call, turning a panic into an error:
// calls run on router goroutines inside the user's login shell, where an
// unrecovered panic would kill the shell and close their terminal tab.
func completeGuarded(ctx context.Context, c Completer, line string, pos int) (result Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			result, err = Result{}, fmt.Errorf("completion: %s completer panicked: %v", c.Name(), p)
		}
	}()
	return c.Complete(ctx, line, pos)
}

// CompleteBounded is Complete for UI callers: it reports the caller's
// context error when the deadline cut completion short with nothing to
// show. Every completer already runs behind the context
// (completeWithBoundary), so one that ignores it cannot block the caller.
func (r *Router) CompleteBounded(ctx context.Context, line string, pos int) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result, err := r.Complete(ctx, line, pos)
	if err == nil && result.Outcome() == OutcomeNotMine && ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	return result, err
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

// PrefetchBounded runs prefetch work in the background. A prefetcher whose
// previous Prefetch has not returned is skipped rather than stacked up.
func (r *Router) PrefetchBounded(line string, pos int) {
	for _, rc := range r.completers {
		p, ok := rc.completer.(Prefetcher)
		if !ok {
			continue
		}
		if !r.prefetching.take(rc.id) {
			continue
		}
		go func(id uint64, prefetcher Prefetcher) {
			defer r.prefetching.release(id)
			// A prefetch bug must not kill the shell; prefetching is
			// best-effort, so the panic is simply dropped.
			defer func() { _ = recover() }()
			prefetcher.Prefetch(line, pos)
		}(rc.id, p)
	}
}
