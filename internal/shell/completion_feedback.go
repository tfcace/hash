package shell

import (
	"context"
	"errors"

	"github.com/tfcace/hash/internal/completion"
	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/parser"
)

// makeEditorCompleteOutcomeFunc adapts the completion router to the editor's
// outcome-aware completion callback, reporting timeouts instead of hiding them.
func makeEditorCompleteOutcomeFunc(router *completion.Router) func(string, int) editor.CompletionOutcome {
	return func(line string, pos int) editor.CompletionOutcome {
		ctx, cancel := context.WithTimeout(context.Background(), editorCompletionTimeout)
		defer cancel()

		result, err := router.CompleteBounded(ctx, line, pos)
		timedOut := errors.Is(err, context.DeadlineExceeded) ||
			(len(result.Items) == 0 && errors.Is(ctx.Err(), context.DeadlineExceeded))
		if err != nil || len(result.Items) == 0 {
			return editor.CompletionOutcome{TimedOut: timedOut}
		}

		items := make([]editor.Completion, len(result.Items))
		for i, item := range result.Items {
			items[i] = editor.Completion{
				Text:        result.Prefix + item.Value,
				Description: item.Description,
			}
		}
		return editor.CompletionOutcome{Items: items}
	}
}

// agentCompleteLinePredicate returns the editor predicate deciding whether Tab
// should submit the line to start inline agent completion. Only inline
// completions qualify: submitting a pipe request would execute the left-hand
// command, which Tab must never do.
func (s *Shell) agentCompleteLinePredicate(lookPath func(string) (string, error)) func(string) bool {
	return func(line string) bool {
		if !s.agentAvailable(lookPath) {
			return false
		}
		return parser.Parse(line).Type == parser.CommandTypeAgentInline
	}
}
