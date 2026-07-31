package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
)

func TestPaceAgentEvents_FlushesContinuousTextOnFrame(t *testing.T) {
	in := make(chan agent.StreamEvent)
	errs := make(chan error)
	ticks := make(chan time.Time)
	out, outErrs := paceAgentEvents(context.Background(), in, errs, agentStreamPacerOptions{ticks: ticks})

	in <- agent.StreamEvent{Type: agent.StreamEventText, Text: "hel"}
	in <- agent.StreamEvent{Type: agent.StreamEventText, Text: "lo"}
	ticks <- time.Now()

	select {
	case event := <-out:
		if event.Type != agent.StreamEventText || event.Text != "hello" {
			t.Fatalf("event = %#v, want one paced hello frame", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for paced frame")
	}

	close(in)
	close(errs)
	for range out {
	}
	for err := range outErrs {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestPaceAgentEvents_FlushesTextBeforeToolEvent(t *testing.T) {
	in := make(chan agent.StreamEvent)
	errs := make(chan error)
	out, _ := paceAgentEvents(context.Background(), in, errs, agentStreamPacerOptions{})

	in <- agent.StreamEvent{Type: agent.StreamEventText, Text: "thinking"}
	in <- agent.StreamEvent{Type: agent.StreamEventToolCall, ToolCall: agent.ToolCallUpdate{ID: "1", Title: "pwd", Status: agent.ToolCallPending}}

	first := <-out
	second := <-out
	if first.Type != agent.StreamEventText || first.Text != "thinking" {
		t.Fatalf("first event = %#v, want flushed text", first)
	}
	if second.Type != agent.StreamEventToolCall || second.ToolCall.Title != "pwd" {
		t.Fatalf("second event = %#v, want tool event", second)
	}

	close(in)
	close(errs)
}

func TestTextStreamFromEvents_ExposesTransientToolStatusOutsideGhostText(t *testing.T) {
	events := make(chan agent.StreamEvent, 2)
	errs := make(chan error)
	events <- agent.StreamEvent{Type: agent.StreamEventToolCall, ToolCall: agent.ToolCallUpdate{ID: "1", Title: "pwd", Status: agent.ToolCallInProgress}}
	events <- agent.StreamEvent{Type: agent.StreamEventText, Text: "ready"}
	close(events)
	close(errs)

	text, streamErrs, status := textStreamFromEvents(events, errs)
	if got := <-status; got != "Agent running pwd..." {
		t.Fatalf("status = %q, want transient tool activity", got)
	}
	if got := <-status; got != "" {
		t.Fatalf("status after text = %q, want cleared activity", got)
	}
	if got := <-text; got != "ready" {
		t.Fatalf("ghost text = %q, want ready", got)
	}
	for err := range streamErrs {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
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
