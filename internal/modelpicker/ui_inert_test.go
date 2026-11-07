package modelpicker

import (
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
)

// Model names and descriptions come from the agent: drawn inert, one row.
func TestPickerUI_DrawsAgentTextInert(t *testing.T) {
	ui := NewPickerUI([]agent.ModelOption{{Value: "m", Name: "evil\x1b]0;pwned\x07", Description: "two\nlines\x1b[2J"}}, "")
	s := ui.View().Content
	for _, raw := range []string{"\x1b]0;pwned", "\x07", "\x1b[2J", "two\nlines"} {
		if strings.Contains(s, raw) {
			t.Errorf("view draws %q raw: %q", raw, s)
		}
	}
}
