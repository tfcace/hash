package shell

import (
	"context"
	"errors"

	"github.com/tfcace/hash/internal/completion"
	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/parser"
)

// makeEditorCompleteOutcomeFunc adapts the completion router to the editor's
// outcome-aware completion callback, reporting timeouts instead of hiding
// them, where in the buffer the items' text replaces, and what the menu
// left out.
func makeEditorCompleteOutcomeFunc(router *completion.Router) func(string, int) editor.CompletionOutcome {
	return func(line string, pos int) editor.CompletionOutcome {
		ctx, cancel := context.WithTimeout(context.Background(), editorCompletionTimeout)
		defer cancel()

		result, err := router.CompleteBounded(ctx, line, pos)
		if err != nil || len(result.Items) == 0 {
			return editor.EmptyOutcome(emptyCompletionStatus(ctx, result.Outcome(), err))
		}

		items := make([]editor.Completion, len(result.Items))
		for i, item := range result.Items {
			items[i] = editor.Completion{
				Text:        item.Value, // the router folds the Prefix in
				Description: item.Description,
			}
		}
		// The router always settles where an answer's replacement starts.
		hidden := result.Hidden()
		return editor.MenuOutcome(items, result.ReplaceFrom).Hiding(editor.CompletionHidden{
			Withheld: hidden.Withheld, More: hidden.More, Uncounted: hidden.Uncounted,
		})
	}
}

// emptyCompletionStatus says why a completion came back without items:
// items hash could not write safely there are withheld, not missing. A
// source still loading wins over a deadline that passed meanwhile: its data
// is on the way and the editor retries when it lands. So does a completer
// that answered "nothing matches" just as the deadline passed: that is its
// answer, not a cut-short one. A completer that does not own the input reads
// as no matches too, like one that owns it and has none, unless the deadline
// is why nobody answered.
func emptyCompletionStatus(ctx context.Context, outcome completion.Outcome, err error) editor.CompletionStatus {
	switch {
	case outcome == completion.OutcomePending:
		return editor.CompletionPending
	case outcome == completion.OutcomeWithheld:
		return editor.CompletionWithheld
	case outcome == completion.OutcomeNoMatches && err == nil:
		return editor.CompletionNoMatches
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return editor.CompletionTimedOut
	}
	return editor.CompletionNoMatches
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
