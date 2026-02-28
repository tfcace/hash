package shell

import (
	"context"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/markdown"
)

type agentStreamCollectionOptions struct {
	initialConversation         bool
	onFirstChunk                func()
	onConversationStart         func()
	writeRendered               func(inConversation bool, rendered string)
	flushDelay                  time.Duration
	stripAwaitingInConversation bool
	stripAwaitingForRender      bool
	trimLeadingNewline          bool
}

type agentStreamCollectionResult struct {
	responseText   string
	lineCount      int
	inConversation bool
	streamErr      error
	canceled       bool
}

// collectAgentStream handles streaming collection, conversation marker stripping,
// optional partial-line flushes, and markdown rendering for both first-turn and
// follow-up conversation requests.
func (s *Shell) collectAgentStream( //nolint:gocyclo // streaming state machine with marker detection
	ctx context.Context,
	textCh <-chan string,
	errCh <-chan error,
	opts agentStreamCollectionOptions,
) agentStreamCollectionResult {
	result := agentStreamCollectionResult{
		inConversation: opts.initialConversation,
	}

	var response strings.Builder
	renderer := markdown.NewStreamingRenderer()

	var markerBuffer strings.Builder
	markerDetected := false
	const markerLen = len(agent.ConversationStartMarker)
	markerPrefix := strings.TrimSpace(agent.ConversationStartMarker)
	firstChunkSeen := false
	var responseMarkerCarry string
	var renderMarkerCarry string
	trimLeadingResponse := opts.trimLeadingNewline
	trimLeadingRender := opts.trimLeadingNewline

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
		opts.writeRendered(result.inConversation, text)
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

			if !markerDetected {
				markerBuffer.WriteString(text)
				buffered := markerBuffer.String()
				trimmedBuffered := strings.TrimSpace(buffered)

				// Wait for enough bytes to decide whether the stream starts with
				// [CONVERSATION], since the marker may arrive split across chunks.
				if len(trimmedBuffered) < markerLen && strings.HasPrefix(markerPrefix, trimmedBuffered) {
					continue
				}
				markerDetected = true

				if agent.HasConversationStart(buffered) {
					if !result.inConversation && opts.onConversationStart != nil {
						opts.onConversationStart()
					}
					result.inConversation = true
					text = agent.StripConversationStart(buffered)
				} else {
					text = buffered
				}
			}

			if result.inConversation {
				stripAwaitingInConversationRender := opts.stripAwaitingForRender || result.inConversation
				responseText := stripConversationMarkersChunk(
					&responseMarkerCarry,
					text,
					opts.stripAwaitingInConversation,
				)
				responseText = trimLeadingSingleNewline(responseText, &trimLeadingResponse)
				appendResponse(responseText)

				renderText := stripConversationMarkersChunk(
					&renderMarkerCarry,
					text,
					stripAwaitingInConversationRender,
				)
				renderText = trimLeadingSingleNewline(renderText, &trimLeadingRender)
				writeRendered(renderer.Write(renderText))
				if flushTimer != nil {
					resetStreamFlushTimer(flushTimer, opts.flushDelay)
				}
				continue
			}

			appendResponse(text)
			renderText := text
			if opts.stripAwaitingForRender {
				renderText = strings.ReplaceAll(renderText, agent.AwaitingInputMarker, "")
			}
			writeRendered(renderer.Write(renderText))

			if flushTimer != nil {
				resetStreamFlushTimer(flushTimer, opts.flushDelay)
			}
		}
	}

	// If the stream ended before marker detection resolved (short response),
	// flush buffered content once with the same marker rules.
	if !markerDetected {
		if buffered := markerBuffer.String(); buffered != "" {
			if agent.HasConversationStart(buffered) {
				if !result.inConversation && opts.onConversationStart != nil {
					opts.onConversationStart()
				}
				result.inConversation = true
				buffered = agent.StripConversationStart(buffered)
			}

			if result.inConversation {
				stripAwaitingInConversationRender := opts.stripAwaitingForRender || result.inConversation
				responseText := stripConversationMarkersChunk(
					&responseMarkerCarry,
					buffered,
					opts.stripAwaitingInConversation,
				)
				responseText = trimLeadingSingleNewline(responseText, &trimLeadingResponse)
				appendResponse(responseText)

				renderText := stripConversationMarkersChunk(
					&renderMarkerCarry,
					buffered,
					stripAwaitingInConversationRender,
				)
				renderText = trimLeadingSingleNewline(renderText, &trimLeadingRender)
				writeRendered(renderer.Write(renderText))
				buffered = ""
			}

			if buffered != "" {
				appendResponse(buffered)
				renderText := buffered
				if opts.stripAwaitingForRender {
					renderText = strings.ReplaceAll(renderText, agent.AwaitingInputMarker, "")
				}
				writeRendered(renderer.Write(renderText))
			}
		}
	}

	if result.inConversation {
		stripAwaitingInConversationRender := opts.stripAwaitingForRender || result.inConversation
		responseTail := flushConversationMarkerCarry(&responseMarkerCarry, opts.stripAwaitingInConversation)
		responseTail = trimLeadingSingleNewline(responseTail, &trimLeadingResponse)
		appendResponse(responseTail)

		renderTail := flushConversationMarkerCarry(&renderMarkerCarry, stripAwaitingInConversationRender)
		renderTail = trimLeadingSingleNewline(renderTail, &trimLeadingRender)
		writeRendered(renderer.Write(renderTail))
	}

	writeRendered(renderer.Flush())
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

func stripConversationMarkersChunk(carry *string, chunk string, stripAwaiting bool) string {
	combined := *carry + chunk
	if combined == "" {
		return ""
	}

	hold := longestConversationMarkerPrefixSuffix(combined, stripAwaiting)
	emit := combined
	if hold > 0 {
		emit = combined[:len(combined)-hold]
		*carry = combined[len(combined)-hold:]
	} else {
		*carry = ""
	}

	for _, marker := range conversationMarkers(stripAwaiting) {
		emit = strings.ReplaceAll(emit, marker, "")
	}
	return emit
}

func flushConversationMarkerCarry(carry *string, stripAwaiting bool) string {
	if *carry == "" {
		return ""
	}
	emit := *carry
	*carry = ""
	for _, marker := range conversationMarkers(stripAwaiting) {
		emit = strings.ReplaceAll(emit, marker, "")
	}
	return emit
}

func longestConversationMarkerPrefixSuffix(text string, stripAwaiting bool) int {
	maxHold := 0
	for _, marker := range conversationMarkers(stripAwaiting) {
		// If the full marker is already complete at the end, don't hold any of it.
		// Let the caller emit this chunk and strip the full marker immediately.
		if strings.HasSuffix(text, marker) {
			continue
		}
		maxPrefix := len(marker) - 1
		if maxPrefix > len(text) {
			maxPrefix = len(text)
		}
		for n := maxPrefix; n >= 1; n-- {
			if strings.HasSuffix(text, marker[:n]) {
				if n > maxHold {
					maxHold = n
				}
				break
			}
		}
	}
	return maxHold
}

func conversationMarkers(stripAwaiting bool) []string {
	if stripAwaiting {
		return []string{agent.ConversationStartMarker, agent.AwaitingInputMarker}
	}
	return []string{agent.ConversationStartMarker}
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
