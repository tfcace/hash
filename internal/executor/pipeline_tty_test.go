package executor

import (
	"slices"
	"strings"
	"testing"
)

// A pipeline's last command writes to the shell's terminal, and the terminal
// stays cooked: the pipeline's other commands use the terminal too (`stty -a
// | cat` reads its settings, a sibling's stderr writes to it), and a pager
// at the tail reads its keys from /dev/tty itself, as in any shell.
func TestExecute_PipelineTailLeavesTheTerminalCooked(t *testing.T) {
	t.Parallel()
	got := ttyShell(t, `{ sleep 0.3; stty -a; } | cat`)
	flags := strings.Fields(got)
	for _, flag := range []string{"icanon", "isig", "echo"} {
		if slices.Contains(flags, "-"+flag) || !slices.Contains(flags, flag) {
			t.Fatalf("stty in the pipeline saw %s off:\n%s", flag, got)
		}
	}
	// The tail still writes to a terminal.
	if got := ttyShell(t, `echo x | `+ttyProbe); !strings.Contains(got, "out-tty") {
		t.Fatalf("pipeline tail probe = %q, want out-tty", got)
	}
}
