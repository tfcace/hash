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
