package completion

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCobraCompleter_PrefetchAndComplete(t *testing.T) {
	// Check if kubectl is available for testing
	_, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl not available")
	}

	completer := NewCobraCompleter()
	ctx := context.Background()

	// Before prefetch, Complete should return nothing
	result, err := completer.Complete(ctx, "kubectl get ", 12)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("Expected 0 items before prefetch, got %d", len(result.Items))
	}

	// Trigger prefetch
	completer.Prefetch("kubectl get ", 12)

	// Wait for background prefetch to complete
	time.Sleep(300 * time.Millisecond)

	// Now Complete should return cached results
	result, err = completer.Complete(ctx, "kubectl get ", 12)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should have some completions (pods, deployments, etc.)
	if len(result.Items) > 0 {
		t.Logf("Got %d completions after prefetch", len(result.Items))
	}
}

func TestCobraCompleter_ClampsCursorBeforeSlicing(t *testing.T) {
	completer := NewCobraCompleter()

	if _, err := completer.Complete(context.Background(), "kubectl get ", len("kubectl get ")+10); err != nil {
		t.Fatalf("Complete() with oversized cursor error = %v", err)
	}
	if _, err := completer.Complete(context.Background(), "kubectl get ", -1); err != nil {
		t.Fatalf("Complete() with negative cursor error = %v", err)
	}

	completer.Prefetch("kubectl get ", len("kubectl get ")+10)
	completer.Prefetch("kubectl get ", -1)
}

func TestCobraCompleter_NonCobraCommand(t *testing.T) {
	completer := NewCobraCompleter()
	ctx := context.Background()

	// ls is not a Cobra command - prefetch should fail silently
	completer.Prefetch("ls -", 4)
	time.Sleep(300 * time.Millisecond)

	result, _ := completer.Complete(ctx, "ls -", 4)
	if len(result.Items) != 0 {
		t.Errorf("Items count = %d, want 0 for non-Cobra", len(result.Items))
	}
}

func TestCobraCompleter_RejectsSuccessfulNonCobraOutput(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "echoargs")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", cmdPath, err)
	}

	completer := NewCobraCompleter("echoargs")
	completer.resolvePath = func(string) (string, error) { return cmdPath, nil }
	ready := make(chan struct{}, 4)
	completer.SetOnReady(func() { ready <- struct{}{} })

	line := "echoargs "
	completer.Prefetch(line, len(line))
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("successful non-Cobra probe should notify readiness so pending UI can clear")
	}

	result, _ := completer.Complete(context.Background(), line, len(line))
	if result.Outcome() != OutcomeNotMine {
		t.Fatalf("successful non-Cobra output leaked as %v: %#v", result.Outcome(), result.Items)
	}
	// The failed probe is remembered: another Tab does not rerun it.
	req, _ := completer.request(line, len(line))
	_, st := completer.results.get(context.Background(), req.key(), completer.policy, nil)
	if st != lookupFailed {
		t.Fatalf("failed probe status = %v, want failed (backing off)", st)
	}
}

func TestCobraCompleter_CompleteUsesCacheOnly(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	completer := NewCobraCompleter("fakecobra")
	completer.resolvePath = func(string) (string, error) {
		<-release // a PATH scan stuck on a slow mount
		return "", errors.New("not found")
	}

	start := time.Now()
	result, err := completer.Complete(context.Background(), "fakecobra get ", len("fakecobra get "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Fatalf("Complete blocked %s on command path resolution", elapsed)
	}
	if len(result.Items) != 0 {
		t.Fatalf("Complete() without prefetch should return no items, got %#v", result.Items)
	}
}

// cd is not a tool this completer may ask, so it never runs `cd __complete`.
func TestCobraCompleter_SkipsShellBuiltinCd(t *testing.T) {
	var resolved atomic.Int32
	completer := NewCobraCompleter()
	completer.resolvePath = func(string) (string, error) {
		resolved.Add(1)
		return "/bin/cd", nil
	}

	completer.Prefetch("cd ", len("cd "))
	result, err := completer.Complete(context.Background(), "cd ", len("cd "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 || result.Outcome() != OutcomeNotMine {
		t.Fatalf("cd should bypass Cobra completion, got %#v", result)
	}
	time.Sleep(20 * time.Millisecond)
	if got := resolved.Load(); got != 0 {
		t.Fatalf("shell builtin cd resolved %d times, want 0", got)
	}
}

func TestCobraCompleter_PendingOnMissForKnownCobraTool(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "fakekubectl")
	script := "#!/bin/sh\nif [ \"$1\" = \"__complete\" ]; then printf 'annotate\\tdesc\\napply\\tdesc\\n:4\\n'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(cmdPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatalf("WriteFile: %v", err)
	}

	completer := NewCobraCompleter("fakekubectl")
	completer.resolvePath = func(name string) (string, error) { return cmdPath, nil }
	completer.policy.timeout = 5 * time.Second // Loaded CI machines exceed the production 100ms.
	ready := make(chan struct{}, 8)
	completer.SetOnReady(func() { ready <- struct{}{} })

	waitReady := func(step string) {
		t.Helper()
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no ready notification", step)
		}
	}

	// Warm the subcommand list, as the space-triggered prefetch would.
	completer.Prefetch("fakekubectl ", len("fakekubectl "))
	waitReady("warm prefetch")

	// The user types "a" while the menu is open: novel key, known tool.
	line := "fakekubectl a"
	result, err := completer.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Outcome() != OutcomePending || len(result.Items) != 0 {
		t.Fatalf("result = %+v, want pending with no items (never filenames)", result)
	}

	waitReady("pending fetch")
	result, err = completer.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Outcome() == OutcomePending || len(result.Items) != 2 {
		t.Fatalf("after fetch: result = %+v, want the two candidates", result)
	}
}

// A tool named in the config is asked like a known one: a cache miss reports
// pending and starts the fetch, and the next TAB answers from cache.
func TestCobraCompleter_ConfiguredToolPendsAndFetches(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "plaintool")
	script := "#!/bin/sh\nif [ \"$1\" = \"__complete\" ]; then printf 'sub-a\\tdesc\\n:4\\n'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(cmdPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatalf("WriteFile: %v", err)
	}

	completer := NewCobraCompleter("plaintool")
	completer.resolvePath = func(name string) (string, error) { return cmdPath, nil }
	completer.policy.timeout = 5 * time.Second
	ready := make(chan struct{}, 4)
	completer.SetOnReady(func() { ready <- struct{}{} })

	line := "plaintool a"
	result, err := completer.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Outcome() != OutcomePending {
		t.Fatalf("result = %+v, want pending for a configured tool", result)
	}

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the miss should have started a background fetch")
	}
	result, err = completer.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("second TAB: result = %+v, want the fetched candidate", result)
	}
}

// Well-known cobra tools are seeded: their first-ever miss in a session
// reports pending instead of flashing a filename menu once per tool.
func TestCobraCompleter_SeededToolPendsOnFirstMiss(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "kubectl")
	script := "#!/bin/sh\nif [ \"$1\" = \"__complete\" ]; then printf 'annotate\\tdesc\\n:4\\n'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(cmdPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatalf("WriteFile: %v", err)
	}

	completer := NewCobraCompleter()
	completer.resolvePath = func(name string) (string, error) { return cmdPath, nil }
	completer.policy.timeout = 5 * time.Second
	ready := make(chan struct{}, 4)
	completer.SetOnReady(func() { ready <- struct{}{} })

	line := "kubectl a"
	result, err := completer.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Outcome() != OutcomePending {
		t.Fatalf("result = %+v, want pending for a seeded cobra tool", result)
	}

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("no ready notification")
	}
	result, err = completer.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Outcome() == OutcomePending || len(result.Items) != 1 {
		t.Fatalf("after fetch: result = %+v, want the candidate", result)
	}
}

// The prefetch timeout bounds a background process, not a keystroke. Cold
// kubectl needs hundreds of milliseconds; killing it early fails identically
// on every retry and completion never warms up.
func TestCobraPrefetchTimeoutSurvivesColdStarts(t *testing.T) {
	if cobraPrefetchTimeout < time.Second {
		t.Fatalf("cobraPrefetchTimeout = %v, want at least 1s", cobraPrefetchTimeout)
	}
}

func TestCobraCompleter_Name(t *testing.T) {
	completer := NewCobraCompleter()
	if completer.Name() != "cobra" {
		t.Errorf("Name() = %q, want %q", completer.Name(), "cobra")
	}
}

func TestCobraCompleter_CachesTTL(t *testing.T) {
	completer := NewCobraCompleter()

	// TTL should be set
	if completer.policy.ttl == 0 {
		t.Error("cacheTTL should not be zero")
	}
}

func TestCobraCompleter_NeverBlocks(t *testing.T) {
	completer := NewCobraCompleter()
	ctx := context.Background()

	// Complete should return immediately without results (no prefetch done)
	start := time.Now()
	result, _ := completer.Complete(ctx, "kubectl get ", 12)
	elapsed := time.Since(start)

	// Should complete in under 10ms (just cache lookup)
	if elapsed > 10*time.Millisecond {
		t.Errorf("Complete took %v, expected < 10ms", elapsed)
	}

	if len(result.Items) != 0 {
		t.Errorf("Expected 0 items without prefetch, got %d", len(result.Items))
	}
}

func TestCobraCompleter_PrefetchCoalescesCommandPathLookup(t *testing.T) {
	completer := NewCobraCompleter()
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 4)
	var calls atomic.Int32
	completer.resolvePath = func(name string) (string, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return "", errors.New("not found")
	}

	completer.Prefetch("kubectl get ", len("kubectl get "))
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first command path lookup did not start")
	}

	for _, line := range []string{
		"kubectl describe ",
		"kubectl logs ",
		"kubectl apply ",
	} {
		completer.Prefetch(line, len(line))
	}
	time.Sleep(20 * time.Millisecond)

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one in-flight path lookup for kubectl, got %d", got)
	}
}

// A command path lookup stuck past its timeout (PATH on a dead mount) fails
// the fetch that needed it, and is not doubled by the next prefetch: the
// key keeps its one hung lookup until it returns.
func TestCobraCompleter_HungCommandPathLookupIsNotRelaunched(t *testing.T) {
	completer := NewCobraCompleter()
	completer.pathPolicy.timeout = 50 * time.Millisecond
	completer.policy.failureTTL = 0
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 4)
	var calls atomic.Int32
	completer.resolvePath = func(name string) (string, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return "", errors.New("stale lookup")
	}

	completer.Prefetch("kubectl get ", len("kubectl get "))
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first command path lookup did not start")
	}

	time.Sleep(90 * time.Millisecond)
	completer.Prefetch("kubectl describe ", len("kubectl describe "))
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("hung command path lookup launched %d times, want 1", got)
	}
}

func TestCobraCompleter_RetriesStaleFailedPrefetch(t *testing.T) {
	completer := NewCobraCompleter()
	completer.policy.failureTTL = time.Millisecond
	var calls atomic.Int32
	completer.resolvePath = func(name string) (string, error) {
		calls.Add(1)
		return "", errors.New("temporary lookup failure")
	}

	const line = "kubectl get "
	completer.Prefetch(line, len(line))
	if !eventually(100*time.Millisecond, func() bool {
		return calls.Load() == 1
	}) {
		t.Fatalf("expected first prefetch to attempt path lookup, got %d calls", calls.Load())
	}

	time.Sleep(2 * time.Millisecond)
	completer.Prefetch(line, len(line))
	if !eventually(100*time.Millisecond, func() bool {
		return calls.Load() == 2
	}) {
		t.Fatalf("expected stale failed prefetch to retry, got %d calls", calls.Load())
	}
}

func TestCobraCompleter_PrefetchReturnsWhenChildKeepsStdoutOpen(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "fakecobra")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\nprintf 'pods\\tpod resources\\n'\nsleep 1 &\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", cmdPath, err)
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cobraPrefetchTimeout)
	defer cancel()
	_, _ = runCobraComplete(ctx, cmdPath, []string{"__complete"}, "")
	elapsed := time.Since(start)

	// The WaitDelay releases Wait ~50ms after the script exits; well under
	// the decoy child's 1s sleep and the 3s fetch timeout. The bound leaves
	// headroom for loaded CI machines.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("cobra prefetch waited %s for child-held stdout pipe, want prompt return", elapsed)
	}
}

func TestCobraCompleter_ParseOutputLimitsLargeResultSet(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("resource-")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\tdescription\n")
	}
	b.WriteString(":4\n")

	result, valid := parseCobraOutput(b.String())
	if !valid {
		t.Fatal("parseOutput rejected output with a valid final directive")
	}
	if len(result.Items) > completionItemLimit {
		t.Fatalf("parseOutput returned %d items, want at most %d", len(result.Items), completionItemLimit)
	}
	if len(result.Items) != completionItemLimit {
		t.Fatalf("parseOutput returned %d items, want %d", len(result.Items), completionItemLimit)
	}
	if !result.Hidden().NotShown() {
		t.Fatalf("parseOutput stopped at its limit without saying more were not shown")
	}
	if result.Items[0].Value != "resource-0" || result.Items[len(result.Items)-1].Value != "resource-199" {
		t.Fatalf("parseOutput should preserve first parsed entries, got first=%q last=%q",
			result.Items[0].Value,
			result.Items[len(result.Items)-1].Value,
		)
	}
}

func TestCobraCompleter_ParseOutputValidatesDirective(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		wantValid bool
		wantItems []Item
	}{
		{
			name:   "missing directive",
			output: "pods\tpod resources\n",
		},
		{
			name:   "malformed directive",
			output: "pods\tpod resources\n:not-a-number\n",
		},
		{
			name:   "leading whitespace",
			output: "pods\tpod resources\n :4\n",
		},
		{
			name:   "trailing whitespace",
			output: "pods\tpod resources\n:4 \n",
		},
		{
			name:   "plus sign",
			output: "pods\tpod resources\n:+4\n",
		},
		{
			name:   "minus sign",
			output: "pods\tpod resources\n:-4\n",
		},
		{
			name:   "empty digits",
			output: "pods\tpod resources\n:\n",
		},
		{
			name:   "suffix text",
			output: "pods\tpod resources\n:4suffix\n",
		},
		{
			name:   "Unicode digits",
			output: "pods\tpod resources\n:٤\n",
		},
		{
			name:   "non-final directive",
			output: "pods\tpod resources\n:4\nservices\tservice resources\n",
		},
		{
			name:      "valid empty response",
			output:    ":0\n",
			wantValid: true,
		},
		{
			name:      "valid CRLF response",
			output:    "pods\tpod resources\r\n:4\r\n",
			wantValid: true,
			wantItems: []Item{
				{Value: "pods", Display: "pods", Description: "pod resources"},
			},
		},
		{
			name:      "valid described candidates",
			output:    "pods\tpod resources\nservices\tservice resources\n:4\n\n",
			wantValid: true,
			wantItems: []Item{
				{Value: "pods", Display: "pods", Description: "pod resources"},
				{Value: "services", Display: "services", Description: "service resources"},
			},
		},
		{
			name:      "only final directive is removed",
			output:    ":candidate\tdescription\n:4\n",
			wantValid: true,
			wantItems: []Item{
				{Value: ":candidate", Display: ":candidate", Description: "description"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, valid := parseCobraOutput(tt.output)
			if valid != tt.wantValid {
				t.Fatalf("parseOutput validity = %v, want %v", valid, tt.wantValid)
			}
			if len(result.Items) != len(tt.wantItems) {
				t.Fatalf("parseOutput items = %#v, want %#v", result.Items, tt.wantItems)
			}
			for i := range tt.wantItems {
				if result.Items[i] != tt.wantItems[i] {
					t.Fatalf("parseOutput item %d = %#v, want %#v", i, result.Items[i], tt.wantItems[i])
				}
			}
		})
	}
}

func eventually(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return fn()
}

// Asking a tool for completions runs it: `<cmd> __complete <args>`. For a
// command that is not a Cobra tool that runs the command itself with an extra
// operand, so `rm notes.txt ` (a space, no Enter) deleted notes.txt. Only
// known Cobra tools are ever asked; any other command is never run.
func TestCobraCompleter_NeverRunsAnUnknownCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "RAN")
	cmdPath := filepath.Join(dir, "rmlike")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(cmdPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatalf("WriteFile: %v", err)
	}
	completer := NewCobraCompleter()
	completer.resolvePath = func(string) (string, error) { return cmdPath, nil }
	completer.getwd = func() string { return dir }

	line := "rmlike notes.txt "
	completer.Prefetch(line, len(line)) // the space the user typed
	if _, err := completer.Complete(context.Background(), line, len(line)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("completing an unknown command ran it")
	}
}
