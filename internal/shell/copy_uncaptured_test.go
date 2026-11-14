package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/clipboard"
)

// A pipeline's tail at the prompt writes straight to the terminal, which
// hash never sees: copy out says so, instead of that there is no output
// at some index, and the agent gets no last output.
func TestCopy_OutputThatWentStraightToTheTerminal(t *testing.T) {
	sh, _ := ptyShell(t)
	sh.clipboard = clipboard.NewBuffer(10)
	if _, err := sh.runShellLine(context.Background(), "echo hi | cat"); err != nil {
		t.Fatal(err)
	}
	err := sh.builtinCopy([]string{"out"})
	if err == nil || !strings.Contains(err.Error(), "wrote straight to the terminal") {
		t.Errorf("copy out = %v, want it to say the output went straight to the terminal", err)
	}
	if _, err := sh.runShellLine(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	err = sh.builtinCopy([]string{"out", "2"})
	if err == nil || err.Error() != "command 2 wrote straight to the terminal: hash kept none of its output" {
		t.Errorf("copy out 2 = %v, want it to say command 2 wrote straight to the terminal", err)
	}
	if err := sh.builtinCopy([]string{"out"}); err == nil || err.Error() != "the last command printed nothing" {
		t.Errorf("copy out after true = %v, want it to say it printed nothing", err)
	}
}
