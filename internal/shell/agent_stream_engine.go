package shell

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/markdown"
	"github.com/tfcace/hash/internal/trace"
)

const agentStreamFrameInterval = 40 * time.Millisecond

type agentStreamPacerOptions struct {
	// ticks is test-only. A nil value creates the production fixed-rate ticker.
	ticks <-chan time.Time
}

// paceAgentEvents batches only adjacent text deltas. Unlike a debounce, its
// fixed tick never moves while the agent is busy, so character-sized ACP
// chunks remain visible during a continuous response.
//
//nolint:gocritic,gocyclo // coordinated stream, timer, cancellation, and error state machine
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
		// A request that timed out ends here before the agent says so: name
		// the timeout, or the closed stream reads as an empty reply. A
		// cancel is the turn's own, seen on its context.
		defer func() {
			if err := ctx.Err(); errors.Is(err, context.DeadlineExceeded) {
				select {
				case outErrs <- err:
				default:
				}
			}
		}()

		var ticker *time.Ticker
		ticks := opts.ticks
		if ticks == nil {
			ticker = time.NewTicker(agentStreamFrameInterval)
			defer ticker.Stop()
			ticks = ticker.C
		}

		var pending strings.Builder
		emit := func(event agent.StreamEvent) bool {
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		flush := func() bool {
			if pending.Len() == 0 {
				return true
			}
			if !emit(agent.StreamEvent{Type: agent.StreamEventText, Text: pending.String()}) {
				return false
			}
			pending.Reset()
			return true
		}

		for events != nil || errs != nil {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
				if !flush() {
					return
				}
			case event, ok := <-events:
				if !ok {
					if !flush() {
						return
					}
					events = nil
					continue
				}
				if event.Type == agent.StreamEventText {
					pending.WriteString(event.Text)
					if strings.Contains(event.Text, "\n") && !flush() {
						return
					}
					continue
				}
				if !flush() || !emit(event) {
					return
				}
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if !flush() {
					return
				}
				if err != nil {
					select {
					case outErrs <- err:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return out, outErrs
}

// textStreamFromEvents adapts paced events for editor ghost completion. Tool
// events are intentionally not inserted into the editable command text, but
// their transient status remains visible in the ghost area. The text is the
// fill without the backticks or code fence a model may wrap it in. ended,
// when set, is told how the stream ended (the whole fill, and the error the
// editor was given, nil when the reply completed) before the editor sees the
// channels close.
//
//nolint:gocritic,gocyclo // coordinated event, cancellation, and error state machine
func textStreamFromEvents(ctx context.Context, events <-chan agent.StreamEvent, errs <-chan error, ended func(fill string, err error)) (<-chan editor.GhostStreamUpdate, <-chan error) {
	updates := make(chan editor.GhostStreamUpdate)
	errCh := make(chan error, 1)
	go func() {
		var shown strings.Builder // the fill as the ghost text has it
		var endErr error
		defer close(updates)
		defer close(errCh)
		defer func() {
			if ended != nil {
				ended(shown.String(), endErr)
			}
		}()
		sendUpdate := func(update editor.GhostStreamUpdate) bool {
			select {
			case updates <- update:
				shown.WriteString(update.Text)
				return true
			case <-ctx.Done():
				endErr = context.Cause(ctx)
				return false
			}
		}
		var fill inlineFill
		for events != nil || errs != nil {
			select {
			case <-ctx.Done():
				// The request ended before the stream did (timeout or cancel):
				// tell the editor instead of closing the channels silently,
				// with the reason it ended (an inline tool denied).
				endErr = context.Cause(ctx)
				select {
				case errCh <- endErr:
				default:
				}
				return
			case event, ok := <-events:
				if !ok {
					events = nil
					if rest := fill.end(); rest != "" && !sendUpdate(editor.GhostStreamUpdate{Text: rest}) {
						return
					}
					continue
				}
				if event.Type == agent.StreamEventText && event.Text != "" {
					if text := fill.write(event.Text); text != "" && !sendUpdate(editor.GhostStreamUpdate{Text: text}) {
						return
					}
				} else if event.Type == agent.StreamEventToolCall {
					if event.ToolCall.Status == agent.ToolCallCompleted || event.ToolCall.Status == agent.ToolCallFailed {
						if !sendUpdate(editor.GhostStreamUpdate{}) {
							return
						}
					} else {
						label := inlineToolActivityLabel(event.ToolCall)
						if !sendUpdate(editor.GhostStreamUpdate{Status: label}) {
							return
						}
					}
				}
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if err != nil {
					if ctx.Err() != nil {
						err = context.Cause(ctx) // the stream's own error for its end is just "canceled"
					}
					err = readableAgentErr(err) // the ghost's notice says it in one line
					endErr = err
					select {
					case errCh <- err:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return updates, errCh
}

func inlineToolActivityLabel(update agent.ToolCallUpdate) string {
	title := sanitizeTerminalText(update.Title)
	if title == "" {
		title = "tool"
	}
	return "Agent running " + truncateTerminalText(title, 48) + "..."
}

type agentStreamCollectionOptions struct {
	beforeText    func() // before each text is drawn
	writeRendered func(rendered string)
	wrapWidth     int // columns to word-wrap the reply to; 0 leaves it to the terminal
}

// replyWidth is the width a reply is wrapped to, less the columns a prefix
// drawn at each row start takes, or 0 when stdout is not a terminal.
func replyWidth(prefixCols int) int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w-prefixCols < 20 {
		return 0
	}
	return w - prefixCols
}

type agentStreamCollectionResult struct {
	responseText string
	lineCount    int
	streamErr    error
	canceled     bool
}

// collectAgentEventStream collects a frame-paced event stream. Text frames are
// flushed immediately because pacing has already bounded their latency; tool
// events force a boundary so activity rows never splice into markdown output.
//
//nolint:gocritic,gocyclo // stream, markdown, cancellation, and tool lifecycle state machine
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
	renderer.Width = opts.wrapWidth
	sanitizer := newLegacyAgentMarkerSanitizer()
	wrapper := markdown.NewWrapper(opts.wrapWidth)

	// The turn's trace pairs each raw chunk with what hash drew for it, so a
	// rendering bug can be read off the trace instead of a screen capture.
	started := time.Now()
	traced := trace.Enabled("agent")
	var chunkRendered strings.Builder
	trace.AgentHigh("turn_start", nil)
	defer func() {
		errText := ""
		if result.streamErr != nil {
			errText = result.streamErr.Error()
		}
		trace.AgentHigh("turn_end", map[string]any{
			"ms":            time.Since(started).Milliseconds(),
			"chars":         response.Len(),
			"lines":         result.lineCount,
			"canceled":      result.canceled,
			"error":         errText,
			"rendered_tail": chunkRendered.String(),
		})
	}()

	writeRendered := func(text string) {
		if traced {
			chunkRendered.WriteString(text)
		}
		// Count rows drawn, not lines in the reply: fences draw none and a
		// wrapped line draws several.
		result.lineCount += strings.Count(text, "\n")
		if text == "" || opts.writeRendered == nil {
			return
		}
		// Every time, not once: text that follows a tool call takes the
		// screen back from the spinner the tool started.
		if opts.beforeText != nil {
			opts.beforeText()
		}
		opts.writeRendered(text)
	}
	appendResponse := func(text string) {
		if text == "" {
			return
		}
		response.WriteString(text)
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
				cleanText := sanitizer.Write(event.Text)
				appendResponse(cleanText)
				chunkRendered.Reset()
				writeRendered(wrapper.Write(renderer.Write(cleanText) + renderer.Flush()))
				if traced {
					trace.AgentDetailed("turn_text", map[string]any{
						"raw":      event.Text,
						"clean":    cleanText,
						"rendered": chunkRendered.String(),
					})
				}
			case agent.StreamEventToolCall:
				// The agent's view of the call; a denial is hash's own and is
				// traced at the permission prompt (tool_permission).
				trace.AgentHigh("turn_tool", map[string]any{
					"id":     event.ToolCall.ID,
					"kind":   event.ToolCall.Kind,
					"title":  event.ToolCall.Title,
					"status": string(event.ToolCall.Status),
				})
				// The reply does not continue across a tool row: what the
				// renderer holds (a table, a line start) is drawn before it.
				writeRendered(wrapper.Write(renderer.Finish()) + wrapper.Flush())
				if response.Len() > 0 && !strings.HasSuffix(response.String(), "\n") {
					writeRendered(wrapper.Write("\n"))
				}
				if onToolUpdate != nil {
					onToolUpdate(event.ToolCall)
				}
			}
		}
	}

	// Canceling the turn also closes its stream: when the collector saw the
	// close first, the request was still canceled, not an empty reply.
	if ctx.Err() != nil {
		result.canceled = true
		result.responseText = response.String()
		return result
	}

	chunkRendered.Reset()
	cleanTail := sanitizer.Flush()
	appendResponse(cleanTail)
	writeRendered(wrapper.Write(renderer.Write(cleanTail)+renderer.Finish()) + wrapper.Flush())
	result.lineCount += wrapper.SoftRows()
	result.responseText = response.String()
	return result
}
