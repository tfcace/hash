package completion

import (
	"context"
	"sort"
	"strings"
)

// Router dispatches completion requests to registered completers.
type Router struct {
	completers []registeredCompleter
	fuzzy      bool
}

type registeredCompleter struct {
	completer Completer
	priority  Priority
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
	// Extract the query (word being completed) for fuzzy filtering
	query := extractCompletionQuery(line, pos)

	for _, rc := range r.completers {
		result, err := rc.completer.Complete(ctx, line, pos)
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
			return result, nil
		}
	}

	return Result{}, nil
}

// extractCompletionQuery extracts the word being completed.
func extractCompletionQuery(line string, pos int) string {
	if pos > len(line) {
		pos = len(line)
	}

	// Find start of word (go backwards until space or start)
	start := pos
	for start > 0 && line[start-1] != ' ' && line[start-1] != '\t' {
		start--
	}

	return line[start:pos]
}

// Completers returns the registered completers for inspection.
func (r *Router) Completers() []Completer {
	result := make([]Completer, len(r.completers))
	for i, rc := range r.completers {
		result[i] = rc.completer
	}
	return result
}
