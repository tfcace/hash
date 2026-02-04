package agent

import (
	"context"
	"strings"
)

// StreamingTransport extends Transport with streaming text capability.
type StreamingTransport interface {
	Transport

	// SendStreaming sends a request and returns a channel of text chunks.
	// The channel receives incremental text as it arrives from the agent.
	// The channel is closed when the response is complete.
	SendStreaming(ctx context.Context, req Request) (<-chan string, <-chan error)
}

// StreamRequest sends a request and returns channels for streaming text chunks.
// This provides real-time text as it arrives, before parsing into Response.
// Returns (textChan, errChan) - text chunks arrive on textChan, errors on errChan.
//
//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (c *Client) StreamRequest(ctx context.Context, req Request) (<-chan string, <-chan error) {
	// Check if transport supports streaming
	if st, ok := c.transport.(StreamingTransport); ok {
		return st.SendStreaming(ctx, req)
	}

	// Fallback: use regular Send and emit full response at once
	textCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(textCh)
		defer close(errCh)

		respCh, err := c.transport.Send(ctx, req)
		if err != nil {
			errCh <- err
			return
		}

		for resp := range respCh {
			if resp.Type == ResponseTypeError {
				errCh <- &AgentError{Message: resp.Error}
				return
			}
			// Emit the full text
			if resp.Command != "" {
				textCh <- resp.Command
			} else if resp.Explanation != "" {
				textCh <- resp.Explanation
			}
		}
	}()

	return textCh, errCh
}

// AgentError represents an error from the agent.
type AgentError struct {
	Message string
}

func (e *AgentError) Error() string {
	return e.Message
}

// StreamCollector collects streaming text and determines the final response type.
type StreamCollector struct {
	text strings.Builder
}

// NewStreamCollector creates a new stream collector.
func NewStreamCollector() *StreamCollector {
	return &StreamCollector{}
}

// Append adds text to the collector.
func (c *StreamCollector) Append(text string) {
	c.text.WriteString(text)
}

// Text returns the collected text so far.
func (c *StreamCollector) Text() string {
	return c.text.String()
}

// Response returns the final response based on collected text.
func (c *StreamCollector) Response() Response {
	text := strings.TrimSpace(c.text.String())
	if text == "" {
		return Response{Type: ResponseTypeError, Error: "empty response"}
	}

	if looksLikeCommand(text) {
		return Response{Type: ResponseTypeCommand, Command: text}
	}
	return Response{Type: ResponseTypeExplanation, Explanation: text}
}

// ConversationStartMarker signals the beginning of a multi-turn conversation.
// Appears on the first line to enable conversation UI immediately.
const ConversationStartMarker = "[CONVERSATION]"

// AwaitingInputMarker signals the agent expects user input to continue.
// Appears at the end of a response.
const AwaitingInputMarker = "[AWAITING_INPUT]"

// ProcessAgentResponse strips conversation markers and determines state.
// Returns the display text and whether the agent expects further input.
func ProcessAgentResponse(text string) (display string, expectsInput bool) {
	trimmed := strings.TrimSpace(text)

	// Check for awaiting input marker at end
	if strings.HasSuffix(trimmed, AwaitingInputMarker) {
		trimmed = strings.TrimSuffix(trimmed, AwaitingInputMarker)
		trimmed = strings.TrimSpace(trimmed)
		expectsInput = true
	}

	// Check for conversation start marker at beginning
	if strings.HasPrefix(trimmed, ConversationStartMarker) {
		trimmed = strings.TrimPrefix(trimmed, ConversationStartMarker)
		trimmed = strings.TrimSpace(trimmed)
	}

	return trimmed, expectsInput
}

// HasConversationStart checks if text starts with the conversation marker.
// Used for early detection during streaming.
// Allows leading whitespace (including newlines) before the marker.
func HasConversationStart(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, ConversationStartMarker)
}

// StripConversationStart removes the conversation start marker from text.
// Also removes any leading/trailing whitespace around the marker.
func StripConversationStart(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, ConversationStartMarker) {
		// Remove marker and any following whitespace
		result := strings.TrimPrefix(trimmed, ConversationStartMarker)
		result = strings.TrimSpace(result)
		return result
	}
	return text
}
