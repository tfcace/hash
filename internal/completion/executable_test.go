package completion

import (
	"context"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutableCompleter_Name(t *testing.T) {
	c := NewExecutableCompleter()
	if c.Name() != "executable" {
		t.Errorf("expected name 'executable', got %s", c.Name())
	}
}

func TestExecutableCompleter_CommandPosition(t *testing.T) {
	c := NewExecutableCompleter()
	ctx := context.Background()

	// Should complete at the start of a line
	result, err := c.Complete(ctx, "ls", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have some results (ls should match several commands)
	if len(result.Items) == 0 {
		t.Error("expected some completions for 'ls'")
	}

	// Verify ls is in the results
	found := false
	for _, item := range result.Items {
		if item.Value == "ls" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'ls' in completions")
	}
}

func TestExecutableCompleter_NotInArgPosition(t *testing.T) {
	c := NewExecutableCompleter()
	ctx := context.Background()

	// Should NOT complete after a space (argument position)
	result, err := c.Complete(ctx, "ls -l", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("expected no completions in argument position, got %d", len(result.Items))
	}
}

func TestExecutableCompleter_AfterPipe(t *testing.T) {
	c := NewExecutableCompleter()
	ctx := context.Background()

	// Should complete command after pipe
	result, err := c.Complete(ctx, "cat file | gr", 13)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have results starting with "gr"
	if len(result.Items) == 0 {
		t.Error("expected completions for 'gr' after pipe")
	}

	// Verify grep is in the results
	found := false
	for _, item := range result.Items {
		if item.Value == "grep" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'grep' in completions after pipe")
	}
}

func TestExecutableCompleter_PathPrefix(t *testing.T) {
	c := NewExecutableCompleter()
	ctx := context.Background()

	// Should NOT complete when prefix contains a path (let file completer handle)
	result, err := c.Complete(ctx, "./scr", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("expected no completions for path prefix, got %d", len(result.Items))
	}
}

func TestExecutableCompleter_ReturnsPromptlyWhenColdScanIsSlow(t *testing.T) {
	c := NewExecutableCompleter()
	scanStarted := make(chan struct{})
	releaseScan := make(chan struct{})
	c.coldScanWait = 5 * time.Millisecond
	c.scanExecutables = func() []string {
		close(scanStarted)
		<-releaseScan
		return []string{"slowcmd"}
	}
	defer close(releaseScan)

	start := time.Now()
	result, err := c.Complete(context.Background(), "sl", len("sl"))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case <-scanStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected executable cache refresh to start")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Complete() took %s with a slow cold scan, want under 100ms", elapsed)
	}
	if len(result.Items) != 0 {
		t.Fatalf("cold slow scan should not return speculative items, got %#v", result.Items)
	}
}

func TestExecutableCompleter_ReturnsEmptyWhenContextCanceledBeforeFiltering(t *testing.T) {
	c := NewExecutableCompleter()
	c.cacheTTL = time.Minute
	executables := make([]string, 5000)
	for i := range executables {
		executables[i] = "cmd-" + strconv.Itoa(i)
	}
	c.cache = executables
	c.cacheTime = time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := c.Complete(ctx, "cmd-", len("cmd-"))
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no executable completions after context cancellation, got %d", len(result.Items))
	}
}

func TestExecutableCompleter_StartsFreshRefreshAfterStaleScan(t *testing.T) {
	c := NewExecutableCompleter()
	var calls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	c.coldScanWait = 5 * time.Millisecond
	c.scanExecutables = func() []string {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return []string{"stalecmd"}
		}
		return []string{"freshcmd"}
	}
	defer close(releaseFirst)

	result, err := c.Complete(context.Background(), "fresh", len("fresh"))
	if err != nil {
		t.Fatalf("first Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("first slow scan should not return items, got %#v", result.Items)
	}
	select {
	case <-firstStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected first executable refresh to start")
	}

	time.Sleep(90 * time.Millisecond)
	result, err = c.Complete(context.Background(), "fresh", len("fresh"))
	if err != nil {
		t.Fatalf("second Complete() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected stale executable refresh to be retried, got %d scan calls", got)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "freshcmd" {
		t.Fatalf("second Complete() items = %#v, want freshcmd", result.Items)
	}
}

func TestExecutableCompleter_ScanDoesNotStatPathEntries(t *testing.T) {
	c := NewExecutableCompleter()
	t.Setenv("PATH", "/slow-bin")
	c.readDir = func(dir string) ([]os.DirEntry, error) {
		if dir != "/slow-bin" {
			t.Fatalf("readDir dir = %q, want /slow-bin", dir)
		}
		return []os.DirEntry{
			panicInfoDirEntry{name: "slow-tool"},
		}, nil
	}

	executables := c.scanPATHExecutables()
	if len(executables) != 1 || executables[0] != "slow-tool" {
		t.Fatalf("scanPATHExecutables() = %#v, want slow-tool", executables)
	}
}

func TestExtractPipeContext(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		pos      int
		wantLine string
		wantPos  int
	}{
		{
			name:     "no pipe",
			line:     "ls -la",
			pos:      6,
			wantLine: "ls -la",
			wantPos:  6,
		},
		{
			name:     "single pipe",
			line:     "cat file | grep",
			pos:      15,
			wantLine: "grep",
			wantPos:  4,
		},
		{
			name:     "pipe with spaces",
			line:     "cat file |   gr",
			pos:      15,
			wantLine: "gr",
			wantPos:  2,
		},
		{
			name:     "multiple pipes",
			line:     "cat file | grep foo | wc",
			pos:      24,
			wantLine: "wc",
			wantPos:  2,
		},
		{
			name:     "cursor before pipe",
			line:     "cat f | grep",
			pos:      5,
			wantLine: "cat f | grep",
			wantPos:  5,
		},
		{
			name:     "empty after pipe",
			line:     "cat file | ",
			pos:      11,
			wantLine: "",
			wantPos:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLine, gotPos := ExtractPipeContext(tt.line, tt.pos)
			if gotLine != tt.wantLine {
				t.Errorf("ExtractPipeContext() line = %q, want %q", gotLine, tt.wantLine)
			}
			if gotPos != tt.wantPos {
				t.Errorf("ExtractPipeContext() pos = %d, want %d", gotPos, tt.wantPos)
			}
		})
	}
}
