package shell

import (
	"context"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/markdown"
)

const agentStreamFrameInterval = 40 * time.Millisecond

type agentStreamPacerOptions struct {
	// ticks is test-only. A nil value creates the production fixed-rate ticker.
	ticks <-chan time.Time
}

// paceAgentEvents batches only adjacent text deltas. Unlike a debounce, its
// fixed tick never moves while the agent is busy, so character-sized ACP
// chunks remain visible during a continuous response.
func paceAgentEvents(
	ctx context.Context,
	events <-chan agent.StreamEvent,
	errs <-chan error,
	opts agentStreamPacerOptions,
) (<-chan agent.StreamEvent, <-chan error) {
	out := make(chan agent.StreamEvent, 16)
	outErrs := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(outErrs)

		var ticker *time.Ticker
		ticks := opts.ticks
		if ticks == nil {
			ticker = time.NewTicker(agentStreamFrameInterval)
			defer ticker.Stop()
			ticks = ticker.C
		}

		var pending strings.Builder
		flush := func() {
			if pending.Len() == 0 {
				return
			}
			out <- agent.StreamEvent{Type: agent.StreamEventText, Text: pending.String()}
			pending.Reset()
		}

		for events != nil || errs != nil {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
				flush()
			case event, ok := <-events:
				if !ok {
					flush()
					events = nil
					continue
				}
				if event.Type == agent.StreamEventText {
					pending.WriteString(event.Text)
					if strings.Contains(event.Text, "\n") {
						flush()
					}
					continue
				}
				flush()
				out <- event
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				flush()
				if err != nil {
					outErrs <- err
				}
			}
		}
	}()

	return out, outErrs
}

// textStreamFromEvents adapts paced events for editor ghost completion. Tool
// events are intentionally not inserted into the editable command text, but
// their transient status remains visible in the ghost area.
func textStreamFromEvents(events <-chan agent.StreamEvent, errs <-chan error) (<-chan string, <-chan error, <-chan string) {
	textCh := make(chan string, 16)
	errCh := make(chan error, 1)
	statusCh := make(chan string, 8)
	go func() {
		defer close(textCh)
		defer close(errCh)
		defer close(statusCh)
		sendStatus := func(status string) {
			select {
			case statusCh <- status:
				return
			default:
			}
			// The ghost renderer only needs the newest transient state. Replacing
			// a stale update prevents a fast ACP tool lifecycle from blocking the
			// text stream when the editor is busy redrawing.
			select {
			case <-statusCh:
			default:
			}
			select {
			case statusCh <- status:
			default:
			}
		}
		for events != nil || errs != nil {
			select {
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if event.Type == agent.StreamEventText && event.Text != "" {
					sendStatus("")
					textCh <- event.Text
				} else if event.Type == agent.StreamEventToolCall {
					if event.ToolCall.Status == agent.ToolCallCompleted || event.ToolCall.Status == agent.ToolCallFailed {
						sendStatus("")
					} else {
						sendStatus(inlineToolActivityLabel(event.ToolCall))
					}
				}
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if err != nil {
					errCh <- err
				}
			}
		}
	}()
	return textCh, errCh, statusCh
}

func inlineToolActivityLabel(update agent.ToolCallUpdate) string {
	title := sanitizeTerminalText(update.Title)
	if title == "" {
		title = "tool"
	}
	return "Agent running " + truncateTerminalText(title, 48) + "..."
}

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

// collectAgentEventStream collects a frame-paced event stream. Text frames are
// flushed immediately because pacing has already bounded their latency; tool
// events force a boundary so activity rows never splice into markdown output.
func (s *Shell) collectAgentEventStream(
	ctx context.Context,
	events <-chan agent.StreamEvent,
	errCh <-chan error,
	opts agentStreamCollectionOptions,
	onToolUpdate func(agent.ToolCallUpdate),
) agentStreamCollectionResult {
	var result agentStreamCollectionResult
	var response strings.Builder
	renderer := markdown.NewStreamingRenderer()
	sanitizer := newLegacyAgentMarkerSanitizer()
	trimLeadingResponse := opts.trimLeadingNewline
	firstRender := false

	writeRendered := func(text string) {
		if text == "" || opts.writeRendered == nil {
			return
		}
		if !firstRender {
			firstRender = true
			if opts.onFirstChunk != nil {
				opts.onFirstChunk()
			}
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

	for events != nil || errCh != nil {
		select {
		case <-ctx.Done():
			result.canceled = true
			result.responseText = response.String()
			return result
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil {
				result.streamErr = err
			}
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			switch event.Type {
			case agent.StreamEventText:
				text := trimLeadingSingleNewline(event.Text, &trimLeadingResponse)
				cleanText := sanitizer.Write(text)
				appendResponse(cleanText)
				writeRendered(renderer.Write(cleanText))
				writeRendered(renderer.Flush())
			case agent.StreamEventToolCall:
				writeRendered(renderer.Flush())
				if response.Len() > 0 && !strings.HasSuffix(response.String(), "\n") {
					writeRendered("\n")
				}
				if onToolUpdate != nil {
					onToolUpdate(event.ToolCall)
				}
			}
		}
	}

	cleanTail := sanitizer.Flush()
	appendResponse(cleanTail)
	writeRendered(renderer.Write(cleanTail))
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
