package agent

import (
	"context"
	"strings"
)

// StreamRequest sends a request and returns channels for streaming text chunks.
// This provides real-time text as it arrives, before parsing into Response.
// Returns (textChan, errChan) - text chunks arrive on textChan, errors on errChan.
//
//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (c *Client) StreamRequest(ctx context.Context, req Request) (<-chan string, <-chan error) {
	return c.transport.SendStreaming(ctx, req)
}

// StreamEvents returns ordered text and lifecycle events. Text-only
// transports are adapted so callers can use one stream shape for every agent.
//
//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (c *Client) StreamEvents(ctx context.Context, req Request) (<-chan StreamEvent, <-chan error) {
	if c != nil && c.transport != nil {
		if transport, ok := c.transport.(EventStreamTransport); ok {
			return transport.SendEventStream(ctx, req)
		}
	}

	// Apply backpressure to text-only transports instead of buffering an
	// arbitrary number of chunks while their consumer is busy rendering.
	events := make(chan StreamEvent)
	errs := make(chan error, 1)
	if c == nil || c.transport == nil {
		errs <- &AgentError{Message: "no agent configured"}
		close(events)
		close(errs)
		return events, errs
	}

	textCh, errCh := c.transport.SendStreaming(ctx, req)
	go func() {
		defer close(events)
		defer close(errs)
		for textCh != nil || errCh != nil {
			select {
			case text, ok := <-textCh:
				if !ok {
					textCh = nil
					continue
				}
				if text != "" {
					events <- StreamEvent{Type: StreamEventText, Text: text}
				}
			case err, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				if err != nil {
					errs <- err
				}
			}
		}
	}()

	return events, errs
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
