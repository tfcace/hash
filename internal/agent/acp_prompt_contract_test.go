package agent

import (
	"strings"
	"testing"
)

func TestBuildPromptWithContext_StatesHowRepliesAreShown(t *testing.T) {
	got := buildPromptWithContext(Request{Prompt: "find all Go files modified today"})

	for _, want := range []string{"offers to run", "shown as output", "tool call is denied"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt should say %q, got:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "find all Go files modified today") {
		t.Errorf("the user request should still come last, got:\n%s", got)
	}
}

func TestBuildPromptWithContext_InlineOmitsReplyContract(t *testing.T) {
	got := buildPromptWithContext(Request{
		Prompt: "Complete this shell command argument.",
		Inline: true,
	})

	if strings.Contains(got, "offers to run") {
		t.Errorf("inline completion prompt must not describe the run affordance, got:\n%s", got)
	}
	if !strings.Contains(got, "Complete this shell command argument.") {
		t.Errorf("inline prompt should keep its own instructions, got:\n%s", got)
	}
}
