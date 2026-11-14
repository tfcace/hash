package shell

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/executor"
)

// A background job runs on after the line that started it returns, as in
// bash: `{ sleep 0.2; echo hi > f; } &` at the prompt writes f a moment
// later. Ending the line must not end the job.
func TestExecuteLine_BackgroundJobOutlivesItsLine(t *testing.T) {
	t.Parallel()
	sh := &Shell{executor: executor.New()}
	out := filepath.Join(t.TempDir(), "f")
	line := `{ sleep 0.2; echo hi > ` + out + `; } &`
	if _, err := sh.executeLine(context.Background(), line, io.Discard, io.Discard); err != nil {
		t.Fatalf("executeLine: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && string(b) == "hi\n" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the background job was cut off when its line ended")
}

// The shell ending still ends a line's background job.
func TestExecuteLine_BackgroundJobEndsWithTheShell(t *testing.T) {
	t.Parallel()
	sh := &Shell{executor: executor.New()}
	out := filepath.Join(t.TempDir(), "f")
	ctx, endShell := context.WithCancel(context.Background())
	line := `{ sleep 0.3; echo hi > ` + out + `; } &`
	if _, err := sh.executeLine(ctx, line, io.Discard, io.Discard); err != nil {
		t.Fatalf("executeLine: %v", err)
	}
	endShell()
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(out); err == nil {
		t.Fatal("the background job ran on after the shell ended")
	}
}
