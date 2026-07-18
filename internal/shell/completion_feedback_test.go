package shell

import (
	"context"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/completion"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/editor"
)

type slowTestCompleter struct{}

func (slowTestCompleter) Name() string { return "slow" }

func (slowTestCompleter) Complete(ctx context.Context, line string, pos int) (completion.Result, error) {
	select {
	case <-ctx.Done():
		return completion.Result{}, ctx.Err()
	case <-time.After(2 * time.Second):
		return completion.Result{Items: []completion.Item{{Value: "late"}}}, nil
	}
}

type emptyTestCompleter struct{}

func (emptyTestCompleter) Name() string { return "empty" }

func (emptyTestCompleter) Complete(ctx context.Context, line string, pos int) (completion.Result, error) {
	return completion.Result{}, nil
}

// editorItemsFunc adapts the outcome-based adapter back to a plain items
// function for tests that only care about the items.
func editorItemsFunc(router *completion.Router) func(string, int) []editor.Completion {
	fn := makeEditorCompleteOutcomeFunc(router)
	return func(line string, pos int) []editor.Completion { return fn(line, pos).Items }
}

func TestMakeEditorCompleteOutcomeFunc_ReportsTimeout(t *testing.T) {
	router := completion.NewRouter()
	router.Register(slowTestCompleter{}, completion.PriorityFilesystem)

	fn := makeEditorCompleteOutcomeFunc(router)
	out := fn("git ", 4)

	if !out.TimedOut {
		t.Error("expected TimedOut when the router exceeds its budget")
	}
	if len(out.Items) != 0 {
		t.Errorf("expected no items on timeout, got %d", len(out.Items))
	}
}

func TestMakeEditorCompleteOutcomeFunc_EmptyIsNotTimeout(t *testing.T) {
	router := completion.NewRouter()
	router.Register(emptyTestCompleter{}, completion.PriorityFilesystem)

	fn := makeEditorCompleteOutcomeFunc(router)
	out := fn("git ", 4)

	if out.TimedOut {
		t.Error("a fast empty result is not a timeout")
	}
	if len(out.Items) != 0 {
		t.Errorf("expected no items, got %d", len(out.Items))
	}
}

func TestShell_AgentCompleteLinePredicate(t *testing.T) {
	found := func(string) (string, error) { return "/usr/local/bin/claude-agent-acp", nil }

	s := &Shell{config: config.Default()}
	pred := s.agentCompleteLinePredicate(found)

	if !pred("git log --since ?? last tuesday") {
		t.Error("inline ?? with an available agent should trigger agent completion")
	}
	if pred("?? find large files") {
		t.Error("full agent mode is not an inline completion")
	}
	if pred("ls -la") {
		t.Error("plain command must not trigger agent completion")
	}
	if pred("cat log | ?? extract errors") {
		t.Error("pipe mode must not submit on Tab (submitting executes the left command)")
	}

	noAgent := &Shell{config: &config.Config{Agent: config.AgentConfig{Transport: "stdio", Command: ""}}}
	if noAgent.agentCompleteLinePredicate(found)("git log ?? last tuesday") {
		t.Error("without a reachable agent, Tab must fall through to normal completion")
	}
}
