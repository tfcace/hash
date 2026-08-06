package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/parser"
)

func TestAgentResultRecall_PersistsCompletedFullAndPipeTurns(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, tc := range []struct {
		name   string
		parsed parser.ParseResult
		prompt string
	}{
		{"full", parser.ParseResult{Type: parser.CommandTypeAgent, AgentPrompt: "explain deploy"}, "explain deploy"},
		{"pipe", parser.ParseResult{Type: parser.CommandTypeAgentPipe, Command: "kubectl get pods", AgentPrompt: "summarize"}, "summarize"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			var out strings.Builder
			sh := &Shell{
				config:       cfg,
				history:      store,
				agentHandler: NewAgentHandler(agent.NewClient(agent.NewMockTransport(agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "Deployment is healthy."}))),
				responseUI:   NewResponseUI(&out),
				agentOutput:  NewAgentOutputCoordinator(&out),
			}
			sh.handleAgentFullStreaming(context.Background(), tc.parsed, "test")

			interactions, err := store.GetAgentInteractions(tc.prompt, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(interactions) != 1 {
				t.Fatalf("stored %d interactions, want one", len(interactions))
			}
			if !strings.Contains(interactions[0].Prompt, tc.prompt) || interactions[0].Response != "Deployment is healthy." || interactions[0].ResponseKind != history.AgentResponseKindExplanation {
				t.Fatalf("interaction = %#v", interactions[0])
			}
			if interactions[0].Context != "" {
				t.Fatalf("stored context = %q, want selected context omitted", interactions[0].Context)
			}
		})
	}
}

func TestAgentResultRecall_StoresEffectiveBareErrorPrompt(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := config.Default()
	sh := &Shell{
		config:       cfg,
		history:      store,
		agentHandler: NewAgentHandler(agent.NewClient(agent.NewMockTransport())),
	}
	sh.agentHandler.SetLastError(&LastError{Command: "deploy", Stderr: "permission denied", ExitCode: 1})
	sh.recordAgentResult(
		parser.ParseResult{Type: parser.CommandTypeAgent},
		agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "Use the correct role."},
		"Use the correct role.",
		12,
	)

	interactions, err := store.GetAgentInteractions("permission denied", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(interactions) != 1 || !strings.Contains(interactions[0].Prompt, "Explain this error and suggest a fix") {
		t.Fatalf("effective bare prompt interaction = %#v", interactions)
	}
}

func TestAgentResultRecall_PersistsCompletedConversationTurn(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := config.Default()
	var out strings.Builder
	sh := &Shell{
		config:       cfg,
		history:      store,
		agentHandler: NewAgentHandler(agent.NewClient(agent.NewMockTransport(agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "Use the staging cluster."}))),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
		agentReplyInputHook: func(context.Context) (string, error) {
			return "which cluster should I use", nil
		},
	}

	sh.runAgentConversationLoop(context.Background(), "test", nil)

	interactions, err := store.GetAgentInteractions("which cluster", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(interactions) != 1 || interactions[0].Response != "Use the staging cluster." {
		t.Fatalf("conversation interactions = %#v", interactions)
	}
}
