package shell

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/trace"
)

// readTrace returns the agent-subsystem events written to path, in order.
func readTrace(t *testing.T, path string) []trace.Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer f.Close()
	var entries []trace.Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e trace.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("decode %q: %v", sc.Text(), err)
		}
		if e.Subsystem == "agent" {
			entries = append(entries, e)
		}
	}
	return entries
}

// A ?? turn must be reconstructable from the trace alone: what the agent
// sent, chunk by chunk, what hash rendered for each chunk, the tool calls,
// and how the turn ended.
func TestCollectAgentEventStream_TracesTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("HASH_TRACE", "agent")
	t.Setenv("HASH_TRACE_PATH", path)
	t.Setenv("HASH_TRACE_LEVEL", "detailed")
	if err := trace.Init(); err != nil {
		t.Fatalf("trace.Init() error = %v", err)
	}

	events := make(chan agent.StreamEvent, 4)
	errCh := make(chan error)
	events <- agent.StreamEvent{Type: agent.StreamEventText, Text: "## Hea"}
	events <- agent.StreamEvent{Type: agent.StreamEventText, Text: "ding\n"}
	events <- agent.StreamEvent{Type: agent.StreamEventToolCall, ToolCall: agent.ToolCallUpdate{
		ID: "t1", Kind: "read", Title: "Read go.mod", Status: agent.ToolCallCompleted,
	}}
	events <- agent.StreamEvent{Type: agent.StreamEventText, Text: "done\n"}
	close(events)
	close(errCh)

	sh := &Shell{}
	sh.collectAgentEventStream(context.Background(), events, errCh, agentStreamCollectionOptions{
		writeRendered: func(string) {},
	}, nil)
	trace.Close()

	entries := readTrace(t, path)
	var names []string
	for _, e := range entries {
		names = append(names, e.Event)
	}
	want := []string{"turn_start", "turn_text", "turn_text", "turn_tool", "turn_text", "turn_end"}
	if len(names) != len(want) {
		t.Fatalf("events = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("events = %v, want %v", names, want)
		}
	}

	// Each chunk records what the agent sent and what hash drew for it; the
	// heading split across two chunks must still be drawn as one styled span.
	if raw := entries[1].Data.(map[string]any)["raw"]; raw != "## Hea" {
		t.Errorf("first chunk raw = %q, want %q", raw, "## Hea")
	}
	var drawn strings.Builder
	for _, i := range []int{1, 2, 4} {
		drawn.WriteString(entries[i].Data.(map[string]any)["rendered"].(string))
	}
	drawn.WriteString(entries[5].Data.(map[string]any)["rendered_tail"].(string))
	if !strings.Contains(drawn.String(), "Heading\x1b[0m") {
		t.Errorf("rendered pieces %q do not draw the heading as one span", drawn.String())
	}

	tool := entries[3].Data.(map[string]any)
	if tool["kind"] != "read" || tool["title"] != "Read go.mod" || tool["status"] != "completed" {
		t.Errorf("tool event = %v", tool)
	}

	end := entries[5].Data.(map[string]any)
	if end["chars"] != float64(len("## Heading\ndone\n")) || end["canceled"] != false {
		t.Errorf("turn_end = %v", end)
	}
}
