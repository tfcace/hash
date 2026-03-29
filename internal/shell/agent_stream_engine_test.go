package shell

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCollectAgentStream_TrimLeadingNewlineOnce(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 1)
	errCh := make(chan error)
	textCh <- "\n\nQuestion 1: Is it alive?"
	close(textCh)
	close(errCh)

	var rendered strings.Builder
	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{
		trimLeadingNewline: true,
		writeRendered: func(text string) {
			rendered.WriteString(text)
		},
	})

	if strings.HasPrefix(result.responseText, "\n\n") {
		t.Fatalf("expected at most one leading newline in response, got %q", result.responseText)
	}
	if strings.HasPrefix(rendered.String(), "\n\n") {
		t.Fatalf("expected at most one leading newline in render, got %q", rendered.String())
	}
}

// TestCollectAgentStream_DrainErrChAfterTextClosed verifies that errors sent
// after textCh closes are still captured. Regression test for 86ceee9.
func TestCollectAgentStream_DrainErrChAfterTextClosed(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 2)
	errCh := make(chan error, 1)

	// Send text first, then close textCh, then send error
	textCh <- "partial response"
	close(textCh)
	errCh <- fmt.Errorf("connection reset")
	close(errCh)

	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{})

	if result.responseText != "partial response" {
		t.Errorf("expected 'partial response', got %q", result.responseText)
	}
	if result.streamErr == nil {
		t.Fatal("expected error to be drained after textCh closed")
	}
	if !strings.Contains(result.streamErr.Error(), "connection reset") {
		t.Errorf("expected 'connection reset' error, got: %v", result.streamErr)
	}
}

// TestCollectAgentStream_NoErrorAfterTextClosed verifies that when no error
// is pending after textCh closes, streamErr remains nil.
func TestCollectAgentStream_NoErrorAfterTextClosed(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 1)
	errCh := make(chan error, 1)

	textCh <- "complete response"
	close(textCh)
	close(errCh)

	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{})

	if result.streamErr != nil {
		t.Errorf("expected no error, got: %v", result.streamErr)
	}
}

// TestCollectAgentStream_ContextCancellation verifies that context cancellation
// is properly detected and the result is marked as canceled.
func TestCollectAgentStream_ContextCancellation(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string)
	errCh := make(chan error)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := sh.collectAgentStream(ctx, textCh, errCh, agentStreamCollectionOptions{})

	if !result.canceled {
		t.Fatal("expected canceled=true when context is canceled")
	}
}

// TestCollectAgentStream_ErrorDuringStream verifies that errors arriving
// during active streaming are captured alongside the partial response.
func TestCollectAgentStream_ErrorDuringStream(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 3)
	errCh := make(chan error, 1)

	textCh <- "chunk1"
	textCh <- "chunk2"
	close(textCh)
	errCh <- fmt.Errorf("mid-stream failure")
	close(errCh)

	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{})

	if result.responseText != "chunk1chunk2" {
		t.Errorf("expected 'chunk1chunk2', got %q", result.responseText)
	}
	if result.streamErr == nil {
		t.Fatal("expected stream error")
	}
}

// TestCollectAgentStream_LineCount verifies that lineCount tracks newlines correctly.
func TestCollectAgentStream_LineCount(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 3)
	errCh := make(chan error)

	textCh <- "line1\n"
	textCh <- "line2\nline3\n"
	textCh <- "no newline"
	close(textCh)
	close(errCh)

	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{})

	if result.lineCount != 3 {
		t.Errorf("expected 3 newlines, got %d", result.lineCount)
	}
}
