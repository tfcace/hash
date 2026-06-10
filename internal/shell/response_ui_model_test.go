package shell

import "testing"

func TestAgentStateLabel(t *testing.T) {
	tests := []struct {
		name  string
		state AgentState
		model string
		want  string
	}{
		{"thinking with model", AgentStateThinking, "Sonnet", "agent · thinking · Sonnet"},
		{"thinking no model", AgentStateThinking, "", "agent · thinking"},
		{"connecting with model", AgentStateConnecting, "Haiku", "agent · connecting · Haiku"},
		{"receiving no model", AgentStateReceiving, "", "agent · receiving"},
		// The agent appends " (recommended)" to its default model name; drop it.
		{"strips recommended", AgentStateThinking, "Default (recommended)", "agent · thinking · Default"},
		// Model names can contain brackets (e.g. value "sonnet[1m]"), which is
		// why the label uses "·" instead of wrapping the name in [ ].
		{"name with brackets", AgentStateThinking, "Sonnet (1M context)", "agent · thinking · Sonnet (1M context)"},
		{"only whitespace model", AgentStateThinking, "   ", "agent · thinking"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentStateLabel(tt.state, tt.model); got != tt.want {
				t.Fatalf("agentStateLabel(%v, %q) = %q, want %q", tt.state, tt.model, got, tt.want)
			}
		})
	}
}

func TestSetAgentModelAffectsLabel(t *testing.T) {
	u := NewResponseUI(nil)
	u.SetAgentModel("Opus")
	if u.agentModel != "Opus" {
		t.Fatalf("agentModel = %q, want Opus", u.agentModel)
	}
	u.SetAgentModel("")
	if u.agentModel != "" {
		t.Fatalf("agentModel = %q, want empty", u.agentModel)
	}
}
