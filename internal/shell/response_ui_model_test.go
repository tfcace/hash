package shell

import "testing"

func TestAgentStateLabel(t *testing.T) {
	tests := []struct {
		name  string
		state AgentState
		model string
		want  string
	}{
		{"thinking with model", AgentStateThinking, "Sonnet", "agent · thinking [Sonnet]"},
		{"thinking no model", AgentStateThinking, "", "agent · thinking"},
		{"connecting with model", AgentStateConnecting, "Haiku", "agent · connecting [Haiku]"},
		{"receiving no model", AgentStateReceiving, "", "agent · receiving"},
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
