package agent

import (
	"context"
	"testing"
	"time"
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

// chattyTransport streams chunks until its context ends.
type chattyTransport struct{ *MockTransport }

func (chattyTransport) SendStreaming(ctx context.Context, _ Request) (<-chan string, <-chan error) {
	textCh := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(textCh)
		defer close(errCh)
		for {
			select {
			case textCh <- "x":
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
	}()
	return textCh, errCh
}

// A consumer that stops reading events and cancels must not strand the
// adapter goroutine on an unbuffered send.
func TestClientStreamEvents_AdapterExitsWhenReaderStops(t *testing.T) {
	client := NewClient(chattyTransport{NewMockTransport()})
	ctx, cancel := context.WithCancel(context.Background())
	events, errs := client.StreamEvents(ctx, Request{Prompt: "test"})
	<-events
	cancel()
	select {
	case <-drain(errs):
	case <-time.After(2 * time.Second):
		t.Fatal("StreamEvents adapter still running after cancel with an idle reader")
	}
}
