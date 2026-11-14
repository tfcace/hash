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
	return func(line string, pos int) []editor.Completion { return fn(line, pos).Items() }
}

func TestMakeEditorCompleteOutcomeFunc_ReportsTimeout(t *testing.T) {
	t.Parallel()
	router := completion.NewRouter()
	router.Register(slowTestCompleter{}, completion.PriorityFilesystem)

	fn := makeEditorCompleteOutcomeFunc(router)
	out := fn("git ", 4)

	if out.Status() != editor.CompletionTimedOut {
		t.Error("expected TimedOut when the router exceeds its budget")
	}
	if len(out.Items()) != 0 {
		t.Errorf("expected no items on timeout, got %d", len(out.Items()))
	}
}

func TestMakeEditorCompleteOutcomeFunc_EmptyIsNotTimeout(t *testing.T) {
	router := completion.NewRouter()
	router.Register(emptyTestCompleter{}, completion.PriorityFilesystem)

	fn := makeEditorCompleteOutcomeFunc(router)
	out := fn("git ", 4)

	if out.Status() == editor.CompletionTimedOut {
		t.Error("a fast empty result is not a timeout")
	}
	if len(out.Items()) != 0 {
		t.Errorf("expected no items, got %d", len(out.Items()))
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

// The editor gets one kind per empty outcome; the old pair of bools could
// say both "pending" and "timed out".
func TestEmptyCompletionStatus(t *testing.T) {
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	live := context.Background()

	cases := []struct {
		name    string
		ctx     context.Context
		outcome completion.Outcome
		err     error
		want    editor.CompletionStatus
	}{
		{"not mine", live, completion.OutcomeNotMine, nil, editor.CompletionNoMatches},
		{"no matches", live, completion.OutcomeNoMatches, nil, editor.CompletionNoMatches},
		{"pending", live, completion.OutcomePending, nil, editor.CompletionPending},
		{"deadline error", live, completion.OutcomeNotMine, context.DeadlineExceeded, editor.CompletionTimedOut},
		{"deadline passed", expired, completion.OutcomeNotMine, nil, editor.CompletionTimedOut},
		{"pending past the deadline", expired, completion.OutcomePending, nil, editor.CompletionPending},
		{"no matches past the deadline", expired, completion.OutcomeNoMatches, nil, editor.CompletionNoMatches},
		{"withheld", live, completion.OutcomeWithheld, nil, editor.CompletionWithheld},
	}
	for _, c := range cases {
		if got := emptyCompletionStatus(c.ctx, c.outcome, c.err); got != c.want {
			t.Errorf("%s: status = %v, want %v", c.name, got, c.want)
		}
	}
}

// The outcome says where in the buffer the items' text replaces, which can
// be well after where the editor's own word split would start it.
func TestMakeEditorCompleteOutcomeFunc_ReportsWhereTheReplacementStarts(t *testing.T) {
	router := completion.NewRouter()
	env := completion.NewEnvCompleter(fixedEnv{"HOME=/home/user"})
	router.Register(env, completion.PriorityEnv)

	line := "echo \"a\nb $((1 + $HO"
	out := makeEditorCompleteOutcomeFunc(router)(line, len(line))
	if len(out.Items()) != 1 {
		t.Fatalf("items = %#v, want $HOME", out.Items())
	}
	if got := line[:out.From()] + out.Items()[0].Text; got != "echo \"a\nb $((1 + $HOME" {
		t.Fatalf("applied = %q (from %d), want the reference completed once", got, out.From())
	}
}

type fixedEnv []string

func (e fixedEnv) Environ() []string { return e }

// valuesCompleter completes any line with its values, from the cursor.
type valuesCompleter []string

func (valuesCompleter) Name() string { return "values" }

func (v valuesCompleter) Complete(_ context.Context, _ string, pos int) (completion.Result, error) {
	items := make([]completion.Item, len(v))
	for i, value := range v {
		items[i] = completion.Item{Value: value}
	}
	return completion.Result{Items: items, ReplaceFrom: pos}, nil
}

// The outcome says what the menu left out, so the editor does not put a
// lone survivor in place as if it were the only match.
func TestMakeEditorCompleteOutcomeFunc_SaysWhatTheMenuLeftOut(t *testing.T) {
	router := completion.NewRouter()
	router.Register(valuesCompleter{"My Docs/", "README.md"}, completion.PriorityPlugin)
	line := "eval cat "
	out := makeEditorCompleteOutcomeFunc(router)(line, len(line))
	if len(out.Items()) != 1 || out.Items()[0].Text != "README.md" {
		t.Fatalf("items = %#v, want README.md", out.Items())
	}
	if got := out.Hidden(); got != (editor.CompletionHidden{Withheld: 1}) {
		t.Errorf("hidden = %+v, want one withheld", got)
	}
}
