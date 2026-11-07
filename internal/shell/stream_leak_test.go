package shell

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/parser"
)

// floodTransport sends more events than any buffer holds, then ends.
type floodTransport struct{ namedTestTransport }

func (floodTransport) SendEventStream(ctx context.Context, req agent.Request) (<-chan agent.StreamEvent, <-chan error) {
	events := make(chan agent.StreamEvent, 64)
	for range 64 {
		events <- textEvent("x")
	}
	close(events)
	errs := make(chan error)
	close(errs)
	return events, errs
}

// goroutinesIn counts live goroutines whose stack mentions fn.
func goroutinesIn(fn string) int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), fn)
}

func waitNoGoroutinesIn(t *testing.T, fn string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for goroutinesIn(fn) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutine(s) still in %s after the request ended", goroutinesIn(fn), fn)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A canceled request whose reader is gone must not leave the tracing
// goroutine blocked forever on a send nobody receives.
func TestAgentHandler_StreamEventsStopsWhenTheRequestEnds(t *testing.T) {
	h := NewAgentHandler(agent.NewClient(floodTransport{}))
	ctx, cancel := context.WithCancel(context.Background())
	h.StreamEvents(ctx, agentParse("?? hi"), requestFacts{})
	cancel()
	waitNoGoroutinesIn(t, "(*AgentHandler).StreamEvents.func")
}

// The ghost-text adapter reports an error the editor never reads without
// blocking forever.
func TestTextStreamFromEvents_StopsWhenNobodyReadsTheError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan agent.StreamEvent)
	close(events)
	errs := make(chan error, 2)
	errs <- context.DeadlineExceeded
	errs <- context.DeadlineExceeded
	close(errs)
	textStreamFromEvents(ctx, events, errs, nil)
	cancel()
	waitNoGoroutinesIn(t, "shell.textStreamFromEvents.func")
}

func agentParse(line string) parser.ParseResult { return parser.Parse(line) }
