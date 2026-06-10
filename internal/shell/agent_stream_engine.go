package shell

import (
	"context"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/markdown"
)

type agentStreamCollectionOptions struct {
	onFirstChunk       func()
	writeRendered      func(rendered string)
	flushDelay         time.Duration
	trimLeadingNewline bool
}

type agentStreamCollectionResult struct {
	responseText string
	lineCount    int
	streamErr    error
	canceled     bool
}

// collectAgentStream handles streaming collection, optional partial-line flushes,
// and markdown rendering for agent requests.
//
//nolint:gocyclo // streaming state machine with select loop
func (s *Shell) collectAgentStream(
	ctx context.Context,
	textCh <-chan string,
	errCh <-chan error,
	opts agentStreamCollectionOptions,
) agentStreamCollectionResult {
	var result agentStreamCollectionResult

	var response strings.Builder
	renderer := markdown.NewStreamingRenderer()
	sanitizer := newLegacyAgentMarkerSanitizer()

	firstChunkSeen := false
	trimLeadingResponse := opts.trimLeadingNewline

	var flushTimer *time.Timer
	var flushTimerC <-chan time.Time
	if opts.flushDelay > 0 {
		flushTimer = time.NewTimer(opts.flushDelay)
		if !flushTimer.Stop() {
			select {
			case <-flushTimer.C:
			default:
			}
		}
		flushTimerC = flushTimer.C
		defer flushTimer.Stop()
	}

	writeRendered := func(text string) {
		if text == "" || opts.writeRendered == nil {
			return
		}
		opts.writeRendered(text)
	}

	appendResponse := func(text string) {
		if text == "" {
			return
		}
		response.WriteString(text)
		result.lineCount += strings.Count(text, "\n")
	}

collectLoop:
	for {
		select {
		case <-ctx.Done():
			result.canceled = true
			result.responseText = response.String()
			return result

		case err, ok := <-errCh:
			if !ok {
				errCh = nil // Stop selecting on closed channel
				continue
			}
			if err != nil {
				result.streamErr = err
			}

		case <-flushTimerC:
			// Flush incomplete markdown lines so long partial chunks are visible
			// before things like permission prompts change the screen.
			writeRendered(renderer.Flush())

		case text, ok := <-textCh:
			if !ok {
				break collectLoop
			}

			if !firstChunkSeen {
				firstChunkSeen = true
				if opts.onFirstChunk != nil {
					opts.onFirstChunk()
				}
			}

			responseText := trimLeadingSingleNewline(text, &trimLeadingResponse)
			cleanText := sanitizer.Write(responseText)
			appendResponse(cleanText)
			writeRendered(renderer.Write(cleanText))

			if flushTimer != nil {
				resetStreamFlushTimer(flushTimer, opts.flushDelay)
			}
		}
	}

	cleanTail := sanitizer.Flush()
	appendResponse(cleanTail)
	writeRendered(renderer.Write(cleanTail))

	// Drain any pending error after text channel closed
	select {
	case err := <-errCh:
		if err != nil {
			result.streamErr = err
		}
	default:
	}

	writeRendered(renderer.Finish())
	result.responseText = response.String()
	return result
}

func resetStreamFlushTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

func trimLeadingSingleNewline(text string, pending *bool) string {
	if !*pending || text == "" {
		return text
	}
	switch {
	case strings.HasPrefix(text, "\r\n"):
		*pending = false
		return text[2:]
	case strings.HasPrefix(text, "\n"), strings.HasPrefix(text, "\r"):
		*pending = false
		return text[1:]
	default:
		*pending = false
		return text
	}
}
