package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
)

func TestCollectAgentStream_StripsSplitConversationMarkers(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 3)
	errCh := make(chan error)
	textCh <- "Let me check your Kubernetes contexts.[CONVERSA"
	textCh <- "TION]\n\nYou have two contexts\n[AWAIT"
	textCh <- "ING_INPUT]"
	close(textCh)
	close(errCh)

	var rendered strings.Builder
	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{
		initialConversation:         true,
		stripAwaitingInConversation: true,
		stripAwaitingForRender:      true,
		writeRendered: func(_ bool, text string) {
			rendered.WriteString(text)
		},
	})

	if strings.Contains(result.responseText, agent.ConversationStartMarker) {
		t.Fatalf("response leaked conversation marker: %q", result.responseText)
	}
	if strings.Contains(result.responseText, agent.AwaitingInputMarker) {
		t.Fatalf("response leaked awaiting marker: %q", result.responseText)
	}

	renderedText := rendered.String()
	if strings.Contains(renderedText, agent.ConversationStartMarker) {
		t.Fatalf("render leaked conversation marker: %q", renderedText)
	}
	if strings.Contains(renderedText, agent.AwaitingInputMarker) {
		t.Fatalf("render leaked awaiting marker: %q", renderedText)
	}
}

func TestCollectAgentStream_TrimLeadingNewlineOnce(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 1)
	errCh := make(chan error)
	textCh <- "\n\nQuestion 1: Is it alive?"
	close(textCh)
	close(errCh)

	var rendered strings.Builder
	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{
		initialConversation: true,
		trimLeadingNewline:  true,
		writeRendered: func(_ bool, text string) {
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

func TestCollectAgentStream_StripsCompleteAwaitingMarkerAtChunkEnd(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 1)
	errCh := make(chan error)
	textCh <- "Question 1: Is it an object?\n[AWAITING_INPUT]"
	close(textCh)
	close(errCh)

	var rendered strings.Builder
	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{
		initialConversation:         true,
		stripAwaitingInConversation: true,
		stripAwaitingForRender:      true,
		writeRendered: func(_ bool, text string) {
			rendered.WriteString(text)
		},
	})

	if strings.Contains(result.responseText, agent.AwaitingInputMarker) {
		t.Fatalf("response leaked awaiting marker: %q", result.responseText)
	}
	if strings.Contains(rendered.String(), agent.AwaitingInputMarker) {
		t.Fatalf("render leaked awaiting marker: %q", rendered.String())
	}
}

func TestCollectAgentStream_ConversationForcesRenderMarkerStripping(t *testing.T) {
	sh := &Shell{}

	textCh := make(chan string, 1)
	errCh := make(chan error)
	textCh <- "Question 1: Is it an object?\n[AWAITING_INPUT]"
	close(textCh)
	close(errCh)

	var rendered strings.Builder
	result := sh.collectAgentStream(context.Background(), textCh, errCh, agentStreamCollectionOptions{
		initialConversation:         true,
		stripAwaitingInConversation: true,
		stripAwaitingForRender:      false, // should still be stripped in conversation mode
		writeRendered: func(_ bool, text string) {
			rendered.WriteString(text)
		},
	})

	if strings.Contains(result.responseText, agent.AwaitingInputMarker) {
		t.Fatalf("response leaked awaiting marker: %q", result.responseText)
	}
	if strings.Contains(rendered.String(), agent.AwaitingInputMarker) {
		t.Fatalf("render leaked awaiting marker despite conversation mode: %q", rendered.String())
	}
}
