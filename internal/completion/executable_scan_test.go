package completion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A PATH scan slower than the old 75ms refresh age, with Tabs arriving
// further apart than that, replaced and discarded every scan: the cache
// never filled and command completion never worked.
func TestExecutableCompleter_SlowScanEventuallyAnswers(t *testing.T) {
	c := NewExecutableCompleter()
	c.scanPath = func(string) []string {
		time.Sleep(300 * time.Millisecond)
		return []string{"tool"}
	}

	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		result, _ := c.Complete(ctx, "to", len("to"))
		cancel()
		if len(result.Items) == 1 && result.Items[0].Value == "tool" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("slow PATH scan never filled the executable cache")
}

// In command position a cold scan still running claims the word as pending
// rather than letting filenames answer for a command name.
func TestExecutableCompleter_ColdScanIsPending(t *testing.T) {
	c := NewExecutableCompleter()
	release := make(chan struct{})
	defer close(release)
	c.scanPath = func(string) []string {
		<-release
		return []string{"tool"}
	}

	result, _ := c.Complete(context.Background(), "to", len("to"))
	if got := result.Outcome(); got != OutcomePending {
		t.Fatalf("outcome = %v, want pending while the cold scan runs", got)
	}
}

// A relative $PATH entry ("" is the working directory) names a different
// directory after every cd: the scan for one directory must not answer in
// another.
func TestExecutableCompleter_RelativePathEntryFollowsTheWorkingDirectory(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for dir, name := range map[string]string{a: "only-in-a", b: "only-in-b"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", ":/nonexistent-dir")
	c := NewExecutableCompleter()
	for _, tt := range []struct{ dir, want string }{{a, "only-in-a"}, {b, "only-in-b"}, {a, "only-in-a"}} {
		t.Chdir(tt.dir)
		result, _ := c.Complete(context.Background(), "only", len("only"))
		if len(result.Items) != 1 || result.Items[0].Value != tt.want {
			t.Fatalf("in %s: items = %#v, want %s", tt.dir, result.Items, tt.want)
		}
	}
}
