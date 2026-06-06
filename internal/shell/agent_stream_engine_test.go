package shell

import (
	"context"
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

func TestCollectAgentStream_StripsSplitAwaitingInputMarker(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 3)
	errCh := make(chan error)
	textCh <- "Which directory"
	textCh <- " should I inspect?\n[AWAI"
	textCh <- "TING_INPUT]"
	close(textCh)
	close(errCh)

	var rendered strings.Builder
	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{
		writeRendered: func(text string) {
			rendered.WriteString(text)
		},
	})

	if strings.Contains(result.responseText, "[AWAITING_INPUT]") {
		t.Fatalf("response text leaked marker: %q", result.responseText)
	}
	if strings.Contains(rendered.String(), "[AWAITING_INPUT]") {
		t.Fatalf("rendered text leaked marker: %q", rendered.String())
	}
	if result.responseText != "Which directory should I inspect?\n" {
		t.Fatalf("response text = %q, want marker-free question", result.responseText)
	}
}
