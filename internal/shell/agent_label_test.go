package shell

import (
	"testing"

	"github.com/tfcace/hash/internal/parser"
)

// A plain ?? request is an agent turn: the agent decides whether to run
// commands itself or hand one back, so the label must not promise a command.
func TestAgentRequestLabel(t *testing.T) {
	cases := []struct {
		typ  parser.CommandType
		want string
	}{
		{parser.CommandTypeAgent, "[agent]"},
		{parser.CommandTypeAgentPipe, "[agent: pipe]"},
		{parser.CommandTypeAgentInline, "[agent: inline]"},
	}
	for _, tc := range cases {
		if got := agentRequestLabel(tc.typ); got != tc.want {
			t.Errorf("agentRequestLabel(%v) = %q, want %q", tc.typ, got, tc.want)
		}
	}
}
