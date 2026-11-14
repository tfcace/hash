package shell

import (
	"context"
	"errors"
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

	updates, streamErrs := textStreamFromEvents(context.Background(), events, errs, nil)
	if got := <-updates; got.Status != "Agent running pwd..." || got.Text != "" {
		t.Fatalf("first ghost update = %#v, want transient tool activity", got)
	}
	if got := <-updates; got.Status != "" || got.Text != "ready" {
		t.Fatalf("second ghost update = %#v, want text with cleared activity", got)
	}
	for err := range streamErrs {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}
}

func TestTextStreamFromEvents_StopsWhenContextCanceledWithoutEditorReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := make(chan agent.StreamEvent)
	errs := make(chan error)
	close(errs)

	updates, _ := textStreamFromEvents(ctx, events, errs, nil)
	select {
	case _, ok := <-updates:
		if ok {
			t.Fatal("adapter emitted an update with an already-canceled context")
		}
	case <-time.After(time.Second):
		t.Fatal("adapter did not stop after cancellation")
	}
}

// A request timeout with no fill yet must reach the editor as an error, not
// as a silently closed stream.
func TestTextStreamFromEvents_ReportsContextErrorOnErrChan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	events := make(chan agent.StreamEvent)
	errs := make(chan error)

	_, errCh := textStreamFromEvents(ctx, events, errs, nil)

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("errCh delivered %v, want the deadline error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected the deadline to surface on errCh")
	}
}

// Replies are wrapped by hash, word by word, across chunk boundaries.
func TestCollectAgentEventStream_WrapsToWidth(t *testing.T) {
	events := make(chan agent.StreamEvent, 2)
	errCh := make(chan error)
	events <- agent.StreamEvent{Type: agent.StreamEventText, Text: "aaa bbb c"}
	events <- agent.StreamEvent{Type: agent.StreamEventText, Text: "cc ddd\n"}
	close(events)
	close(errCh)

	var rendered strings.Builder
	sh := &Shell{}
	result := sh.collectAgentEventStream(context.Background(), events, errCh, agentStreamCollectionOptions{
		writeRendered: func(text string) { rendered.WriteString(text) },
		wrapWidth:     8,
	}, nil)

	if got, want := rendered.String(), "aaa bbb\nccc ddd\n"; got != want {
		t.Errorf("rendered = %q, want %q", got, want)
	}
	// Rows drawn, not lines in the reply: erasing by this count must take
	// back exactly what was drawn.
	if result.lineCount != 2 {
		t.Errorf("lineCount = %d, want 2 rows", result.lineCount)
	}
}

// A request that times out before the agent finishes ends the pacer; it must
// say why, or the turn reads the closed stream as an empty reply ("agent
// ended the turn without text") and offers no retry.
func TestPaceAgentEvents_ReportsTheRequestTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	events := make(chan agent.StreamEvent) // the agent never answers
	errs := make(chan error)

	out, outErrs := paceAgentEvents(ctx, events, errs, agentStreamPacerOptions{})
	for range out {
	}
	err := <-outErrs
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pacer error = %v, want the deadline", err)
	}
}

// Ctrl+C closes the request's paced stream as it cancels the turn: the
// collector can see both channels close before it sees the cancel. The
// request was still canceled, not a reply that came back empty.
func TestCollectAgentEventStream_CancelSeenAfterTheStreamClosesIsACancel(t *testing.T) {
	sh := &Shell{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 50 {
		events := make(chan agent.StreamEvent)
		errs := make(chan error)
		close(events)
		close(errs)
		if result := sh.collectAgentEventStream(ctx, events, errs, agentStreamCollectionOptions{}, nil); !result.canceled {
			t.Fatalf("a canceled request ended as %+v", result)
		}
	}
}

// ghostFill streams chunks as an inline reply through textStreamFromEvents
// and returns the fill the ghost text ends up showing, the way the editor
// appends each update.
func ghostFill(t *testing.T, chunks ...string) string {
	t.Helper()
	events := make(chan agent.StreamEvent, len(chunks))
	for _, c := range chunks {
		events <- textEvent(c)
	}
	close(events)
	errs := make(chan error)
	close(errs)
	updates, _ := textStreamFromEvents(context.Background(), events, errs, nil)
	var fill strings.Builder
	for u := range updates {
		fill.WriteString(u.Text)
	}
	return fill.String()
}

// Sonnet wraps an inline fill in backticks; Enter then ran it as command
// substitution (`ls` bare). The fill the user accepts is what is inside.
func TestTextStreamFromEvents_UnwrapsAnInlineFill(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   string
	}{
		{"backticks across chunks", []string{"`internal/", "shell/*.go`"}, "internal/shell/*.go"},
		{"backticks one chunk", []string{"`--all`"}, "--all"},
		{"backtick alone first", []string{"`", "internal/shell/*.go", "`"}, "internal/shell/*.go"},
		{"code fence", []string{"```bash\n", "internal/shell/*.go\n", "```"}, "internal/shell/*.go"},
		{"code fence split", []string{"``", "`sh\ninternal/", "shell/*.go\n`", "``\n"}, "internal/shell/*.go"},
		{"plain", []string{"--format=", "'%h %s'"}, "--format='%h %s'"},
		{"substitution kept", []string{"`pwd`", "/bin"}, "`pwd`/bin"},
		{"inner backtick kept", []string{"$(echo `date`)"}, "$(echo `date`)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fill := ghostFill(t, tc.chunks...)
			if fill != tc.want {
				t.Errorf("fill = %q, want %q", fill, tc.want)
			}
			if tc.name == "backticks across chunks" {
				if line := "ls " + fill; line != "ls internal/shell/*.go" {
					t.Errorf("accepted line = %q", line)
				}
			}
		})
	}
}

// A fill that is not wrapped still streams as it arrives.
func TestTextStreamFromEvents_StreamsAPlainFillAsItArrives(t *testing.T) {
	events := make(chan agent.StreamEvent)
	errs := make(chan error)
	updates, _ := textStreamFromEvents(context.Background(), events, errs, nil)
	events <- textEvent("--al")
	select {
	case u := <-updates:
		if u.Text != "--al" {
			t.Errorf("first update = %q, want the chunk", u.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("a plain chunk was held back")
	}
	close(events)
	close(errs)
}
