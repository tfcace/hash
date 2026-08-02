package shell

import (
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/parser"
)

func TestRecordAgentResult_DefaultAndOptOut(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sh := &Shell{history: store, config: config.Default()}

	sh.recordAgentResult(parser.ParseResult{Type: parser.CommandTypeAgent, AgentPrompt: "find logs"}, agent.Response{
		Type: agent.ResponseTypeCommand, Command: "rg --files | rg log",
	}, "rg --files | rg log", 25*time.Millisecond)
	interactions, err := store.GetAgentInteractions("find logs", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(interactions) != 1 {
		t.Fatalf("default interaction count = %d, want 1", len(interactions))
	}

	sh.config.History.AgentResultsEnabled = false
	sh.recordAgentResult(parser.ParseResult{Type: parser.CommandTypeAgentPipe, AgentPrompt: "skip logs"}, agent.Response{
		Type: agent.ResponseTypeCommand, Command: "rg --files | rg log",
	}, "rg --files | rg log", 25*time.Millisecond)
	interactions, err = store.GetAgentInteractions("find logs", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(interactions) != 1 || interactions[0].ResponseKind != history.AgentResponseKindCommand || interactions[0].Context != "" || interactions[0].LatencyMs != 25 {
		t.Fatalf("stored interaction = %#v, want default command result without selected context", interactions)
	}
	skipped, err := store.GetAgentInteractions("skip logs", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("explicit opt-out interaction count = %d, want 0", len(skipped))
	}

	sh.markLatestAgentResultAccepted()
	interactions, err = store.GetAgentInteractions("find logs", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !interactions[0].Accepted {
		t.Fatal("Run/Edit acceptance should be tracked for the stored result")
	}
}

func TestRecordAgentResult_SkipsInlineErrorsAndEmptyResponses(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sh := &Shell{history: store, config: config.Default()}

	for _, tc := range []struct {
		name   string
		parsed parser.ParseResult
		resp   agent.Response
		text   string
	}{
		{"inline", parser.ParseResult{Type: parser.CommandTypeAgentInline, AgentPrompt: "complete"}, agent.Response{Type: agent.ResponseTypeCommand, Command: "value"}, "value"},
		{"error", parser.ParseResult{Type: parser.CommandTypeAgent, AgentPrompt: "fix"}, agent.Response{Type: agent.ResponseTypeError, Error: "failed"}, "partial"},
		{"empty", parser.ParseResult{Type: parser.CommandTypeAgent, AgentPrompt: "fix"}, agent.Response{Type: agent.ResponseTypeCommand}, "  \n\t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh.recordAgentResult(tc.parsed, tc.resp, tc.text, time.Millisecond)
		})
	}

	interactions, err := store.GetAgentInteractions("", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(interactions) != 0 {
		t.Fatalf("non-recallable turns stored = %#v, want none", interactions)
	}
}

func TestRecordCommand_Builtin(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	s := &Shell{history: store}

	// Simulate builtin execution (cd, history, etc.)
	s.recordCommand("cd /tmp", 0, 0)
	s.recordCommand("history", 0, 0)

	recent, err := store.GetRecent(10)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(recent))
	}
	if recent[0].Command != "history" {
		t.Errorf("most recent = %q, want %q", recent[0].Command, "history")
	}
	if recent[1].Command != "cd /tmp" {
		t.Errorf("second = %q, want %q", recent[1].Command, "cd /tmp")
	}
}

func TestRecordCommand_AgentPrompt(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	s := &Shell{history: store}

	// Simulate agent invocation — the raw ?? line is recorded
	s.recordCommand("?? find large files", 0, 0)

	recent, err := store.GetRecent(10)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(recent))
	}
	if recent[0].Command != "?? find large files" {
		t.Errorf("command = %q, want %q", recent[0].Command, "?? find large files")
	}
}

func TestRecordCommand_AgentPipePrompt(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	s := &Shell{history: store}

	s.recordCommand("cat log.txt | ?? summarize errors", 0, 0)

	recent, err := store.GetRecent(10)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(recent))
	}
	if recent[0].Command != "cat log.txt | ?? summarize errors" {
		t.Errorf("command = %q, want %q", recent[0].Command, "cat log.txt | ?? summarize errors")
	}
}

func TestRecordCommand_ExternalCommand(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	s := &Shell{history: store}

	// Regular external command with exit code and duration
	s.recordCommand("make build", 2, 3*time.Second)

	recent, err := store.GetRecent(10)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(recent))
	}
	if recent[0].Command != "make build" {
		t.Errorf("command = %q, want %q", recent[0].Command, "make build")
	}
	if recent[0].ExitCode != 2 {
		t.Errorf("exit code = %d, want 2", recent[0].ExitCode)
	}
	if recent[0].DurationMs != 3000 {
		t.Errorf("duration = %d ms, want 3000", recent[0].DurationMs)
	}
}

func TestRecordCommand_NilHistory(t *testing.T) {
	s := &Shell{history: nil}

	// Should not panic
	s.recordCommand("cd /tmp", 0, 0)
}

func TestRecordCommand_SudoBuiltin(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	s := &Shell{history: store}

	s.recordCommand("sudo ls /root", 0, 0)

	recent, err := store.GetRecent(10)
	if err != nil {
		t.Fatalf("GetRecent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(recent))
	}
	// sudo prefix should be stripped from Command but preserved in RawCommand
	if recent[0].Command != "ls /root" {
		t.Errorf("command = %q, want %q (sudo stripped)", recent[0].Command, "ls /root")
	}
	if recent[0].RawCommand != "sudo ls /root" {
		t.Errorf("raw = %q, want %q", recent[0].RawCommand, "sudo ls /root")
	}
	if !recent[0].IsSudo {
		t.Error("IsSudo should be true")
	}
}
