package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
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

func TestCollectAgentStream_StripsAwaitingMarkerFromRender(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 1)
	errCh := make(chan error)
	textCh <- "Question 1: Is it an object?\n[AWAITING_INPUT]"
	close(textCh)
	close(errCh)

	var rendered strings.Builder
	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{
		stripAwaitingForRender: true,
		writeRendered: func(text string) {
			rendered.WriteString(text)
		},
	})

	// Response text keeps the marker (it's stripped at a higher level if needed)
	_ = result

	if strings.Contains(rendered.String(), agent.AwaitingInputMarker) {
		t.Fatalf("render leaked awaiting marker: %q", rendered.String())
	}
}
