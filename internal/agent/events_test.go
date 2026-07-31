package agent

import (
	"context"
	"testing"
)

func TestClientStreamEvents_AdaptsTextOnlyTransport(t *testing.T) {
	client := NewClient(NewMockTransport(Response{Type: ResponseTypeExplanation, Explanation: "hello"}))

	events, errs := client.StreamEvents(context.Background(), Request{Prompt: "test"})
	var got []StreamEvent
	for event := range events {
		got = append(got, event)
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("StreamEvents error: %v", err)
		}
	}

	if len(got) != 1 {
		t.Fatalf("event count = %d, want 1", len(got))
	}
	if got[0].Type != StreamEventText || got[0].Text != "hello" {
		t.Fatalf("event = %#v, want text event", got[0])
	}
}

func TestToolCallUpdateMergePreservesPriorFields(t *testing.T) {
	initial := ToolCallUpdate{ID: "call-1", Title: "pwd", Kind: "execute", Status: ToolCallPending}
	merged := initial.Merge(ToolCallUpdate{ID: "call-1", Status: ToolCallCompleted})

	if merged.Title != "pwd" || merged.Kind != "execute" || merged.Status != ToolCallCompleted {
		t.Fatalf("merged update = %#v", merged)
	}
}
