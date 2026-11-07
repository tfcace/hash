package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/config"
)

// A closed stdin ends the shell. The editor used to report it as a Ctrl+C,
// so the loop printed "^C" and opened a new editor on the same closed input
// forever (`printf 'echo hi\n' | hash` wrote hundreds of MB a minute).
// Not parallel: it swaps os.Stdin and os.Stdout.
func TestShell_ClosedStdinEndsTheLoop(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("echo hi\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	outPath := filepath.Join(t.TempDir(), "out")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = r, out
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut; r.Close(); out.Close() }()

	// Built after the swap: the shell keeps os.Stdout for its prompt marks.
	cfg := config.Default()
	cfg.History.Enabled = false
	cfg.Prompt.Mode = "none"
	sh, err := NewWithMode(cfg, Mode{})
	if err != nil {
		t.Fatalf("NewWithMode() error = %v", err)
	}
	defer sh.Close() //nolint:errcheck // test cleanup

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runErr := sh.Run(ctx)
	os.Stdin, os.Stdout = oldIn, oldOut

	if ctx.Err() != nil {
		t.Fatalf("the shell loop did not end on closed stdin (Run = %v)", runErr)
	}
	if runErr != nil {
		t.Errorf("Run() = %v, want nil", runErr)
	}
	got, _ := os.ReadFile(outPath)
	if !strings.Contains(string(got), "hi\n") {
		t.Errorf("the piped line did not run; output %q", got)
	}
	if strings.Contains(string(got), "^C") {
		t.Errorf("closed input read as Ctrl+C; output %q", got)
	}
}
