package completion

import (
	"context"
	"sort"
)

// Router dispatches completion requests to registered completers.
type Router struct {
	completers []registeredCompleter
}

type registeredCompleter struct {
	completer Completer
	priority  Priority
}

// NewRouter creates a new completion router.
func NewRouter() *Router {
	return &Router{}
}

// Register adds a completer with the given priority.
// Lower priority values are tried first.
func (r *Router) Register(c Completer, priority Priority) {
	r.completers = append(r.completers, registeredCompleter{
		completer: c,
		priority:  priority,
	})

	// Sort by priority (lower first)
	sort.Slice(r.completers, func(i, j int) bool {
		return r.completers[i].priority < r.completers[j].priority
	})
}

// Complete tries each completer in priority order until one returns results.
func (r *Router) Complete(ctx context.Context, line string, pos int) (Result, error) {
	for _, rc := range r.completers {
		result, err := rc.completer.Complete(ctx, line, pos)
		if err != nil {
			continue // Try next completer on error
		}

		if len(result.Items) > 0 {
			return result, nil
		}
	}

	return Result{}, nil
}

// Completers returns the registered completers for inspection.
func (r *Router) Completers() []Completer {
	result := make([]Completer, len(r.completers))
	for i, rc := range r.completers {
		result[i] = rc.completer
	}
	return result
}
