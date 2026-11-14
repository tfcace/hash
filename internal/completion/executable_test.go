package completion

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
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

// Registered builtins complete in command position, labeled, deduplicated
// against PATH executables of the same name, and prefix-filtered.
func TestExecutableCompleter_CompletesBuiltins(t *testing.T) {
	c := NewExecutableCompleter()
	c.scanPath = func(string) []string { return []string{"completions", "compgen", "ls"} }
	c.SetBuiltins([]string{"completions", "cd"})
	ctx := context.Background()

	result, err := c.Complete(ctx, "comp", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var values []string
	builtinCount := 0
	for _, item := range result.Items {
		values = append(values, item.Value)
		if item.Value == "completions" {
			builtinCount++
			if item.Description != "hash builtin" {
				t.Errorf("completions description = %q, want hash builtin", item.Description)
			}
		}
	}
	if builtinCount != 1 {
		t.Errorf("completions appeared %d times in %v, want exactly once", builtinCount, values)
	}
	for _, unwanted := range []string{"cd", "ls"} {
		for _, v := range values {
			if v == unwanted {
				t.Errorf("%q should not match prefix comp: %v", unwanted, values)
			}
		}
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
	c.policy.wait = 5 * time.Millisecond
	c.scanPath = func(string) []string {
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

// A slow completer earlier in the router can eat most of the Tab budget, so
// the deadline may pass while a cold scan is waited on. The scan still
// runs and will notify ready: that is pending, not a context error the
// router drops.
func TestExecutableCompleter_ReportsPendingWhenTheDeadlinePassesDuringAColdScan(t *testing.T) {
	c := NewExecutableCompleter()
	release := make(chan struct{})
	defer close(release)
	c.scanPath = func(string) []string {
		<-release
		return []string{"slowcmd"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	result, err := c.Complete(ctx, "sl", len("sl"))
	if err != nil || result.Outcome() != OutcomePending {
		t.Fatalf("Complete = %v (err %v), want pending with no error", result.Outcome(), err)
	}
}

func TestExecutableCompleter_ReturnsEmptyWhenContextCanceledBeforeFiltering(t *testing.T) {
	c := NewExecutableCompleter()
	executables := make([]string, 5000)
	for i := range executables {
		executables[i] = "cmd-" + strconv.Itoa(i)
	}
	c.scanPath = func(string) []string { return executables }
	if _, st := c.getExecutables(context.Background()); st != lookupFresh {
		t.Fatalf("seeding scan = %v, want fresh", st)
	}

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

// A scan that outlives its timeout (directory reads cannot be canceled)
// fails, but is not doubled by a rescan that would hang on the same mount.
// When it finally returns, its listing answers.
func TestExecutableCompleter_HungScanIsNotRelaunchedAndItsLateListingAnswers(t *testing.T) {
	c := NewExecutableCompleter()
	c.policy.timeout = 50 * time.Millisecond
	c.policy.failureTTL = 10 * time.Millisecond
	c.policy.wait = 5 * time.Millisecond
	var calls atomic.Int32
	release := make(chan struct{})
	c.scanPath = func(string) []string {
		calls.Add(1)
		<-release
		return []string{"latecmd"}
	}

	for range 10 {
		_, _ = c.Complete(context.Background(), "late", len("late"))
		time.Sleep(20 * time.Millisecond)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("hung scan launched %d times, want 1", got)
	}

	close(release)
	var result Result
	if !eventuallyTrue(time.Second, func() bool {
		result, _ = c.Complete(context.Background(), "late", len("late"))
		return len(result.Items) > 0
	}) || result.Items[0].Value != "latecmd" {
		t.Fatalf("after the hung scan returned: items = %#v, want latecmd", result.Items)
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

	executables := c.scanPATHExecutables(os.Getenv("PATH"))
	if len(executables) != 1 || executables[0] != "slow-tool" {
		t.Fatalf("scanPATHExecutables() = %#v, want slow-tool", executables)
	}
}

// A PATH directory whose read never returns (dead NFS or sshfs mount) must
// not leave command-position completion pending forever: the scan fails at
// its timeout and the UI is told.
func TestExecutableCompleter_HungPATHScanEndsPending(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	c := NewExecutableCompleter()
	c.policy.timeout = 50 * time.Millisecond
	c.policy.wait = 5 * time.Millisecond
	c.scanPath = func(string) []string { <-block; return nil }
	ready := make(chan struct{}, 4)
	c.SetOnReady(func() { ready <- struct{}{} })

	if r, _ := c.Complete(context.Background(), "zz", 2); r.Outcome() != OutcomePending {
		t.Fatalf("cold Tab = %v, want pending", r.Outcome())
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("no ready notification when the hung scan timed out")
	}
	if r, _ := c.Complete(context.Background(), "zz", 2); r.Outcome() == OutcomePending {
		t.Fatal("Tab after the scan timed out is still pending")
	}
}

// A hung PATH scan must not hide the builtins for good: once the scan has
// timed out, the builtins are offered (the ready notification makes the
// "fetching" menu retry by itself). While the scan is still within its
// timeout the Tab stays pending: answering with builtins alone would put a
// lone builtin in place ("his" -> "history") over PATH matches the scan has
// not delivered yet, and nothing would correct it.
func TestExecutableCompleter_OffersBuiltinsOnceHungPATHScanTimesOut(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	c := NewExecutableCompleter()
	c.SetBuiltins([]string{"history"})
	c.policy.timeout = 50 * time.Millisecond
	c.policy.wait = 5 * time.Millisecond
	c.scanPath = func(string) []string { <-block; return nil }

	if r, _ := c.Complete(context.Background(), "his", 3); r.Outcome() != OutcomePending {
		t.Fatalf("Tab while the scan runs = %v %#v, want pending", r.Outcome(), r.Items)
	}
	var r Result
	if !eventuallyTrue(time.Second, func() bool {
		r, _ = c.Complete(context.Background(), "his", 3)
		return len(r.Items) > 0
	}) {
		t.Fatalf("after the scan timed out: %v %#v, want the history builtin", r.Outcome(), r.Items)
	}
	if len(r.Items) != 1 || r.Items[0].Value != "history" {
		t.Fatalf("items = %#v, want history", r.Items)
	}
}

// A function or alias sharing a prefix with PATH executables must not hide
// them: with a function gitlog, Tab on "gi" offering only gitlog put it in
// place of the git the user was typing.
func TestExecutableCompleter_OffersFunctionsAlongsideExecutables(t *testing.T) {
	exe := NewExecutableCompleter()
	exe.scanPath = func(string) []string { return []string{"git", "gitk"} }
	exe.SetBuiltins([]string{"history"})
	exe.SetFunctions(&mockFunctionProvider{functions: []string{"gitlog", "git", "ll"}})
	router := NewRouter()
	router.Register(exe, PriorityExecutable)

	result, _ := router.Complete(context.Background(), "gi", 2)
	var got []string
	for _, item := range result.Items {
		got = append(got, item.Value+":"+item.Description)
	}
	want := []string{"git:function", "gitlog:function", "gitk:"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("Tab on gi = %q, want %q (functions first, then executables they do not shadow)", got, want)
	}
	if r, _ := exe.Complete(context.Background(), "ls l", 4); r.Outcome() != OutcomeNotMine {
		t.Fatalf("functions offered in argument position: %#v", r.Items)
	}
}

// mockFunctionProvider implements FunctionProvider for tests.
type mockFunctionProvider struct {
	functions []string
}

func (m *mockFunctionProvider) Functions() []string {
	return m.functions
}

// Functions match as the shell defines them, case-sensitively, in command
// position only (after a pipe too), and a long list is capped.
func TestExecutableCompleter_FunctionMatching(t *testing.T) {
	many := make([]string, 5000)
	for i := range many {
		many[i] = "fn-" + strconv.Itoa(i)
	}
	tests := []struct {
		name      string
		functions []string
		line      string
		want      int
	}{
		{"case-sensitive", []string{"MyFunc", "myfunc"}, "my", 1},
		{"after a pipe", []string{"myfunc", "other"}, "echo hi | my", 1},
		{"not an argument", []string{"site", "serve"}, "cd si", 0},
		{"capped", many, "fn-", completionItemLimit},
	}
	for _, tt := range tests {
		exe := NewExecutableCompleter()
		exe.scanPath = func(string) []string { return nil }
		exe.SetFunctions(&mockFunctionProvider{functions: tt.functions})
		result, _ := exe.Complete(context.Background(), tt.line, len(tt.line))
		if len(result.Items) != tt.want {
			t.Errorf("%s: Complete(%q) = %d items, want %d", tt.name, tt.line, len(result.Items), tt.want)
		}
	}
}

// Functions come in name order whatever order the shell lists them in (a
// map's), so the menu is the same on every Tab.
func TestExecutableCompleter_OffersFunctionsInNameOrder(t *testing.T) {
	c := NewExecutableCompleter()
	c.scanPath = func(string) []string { return nil }
	c.SetFunctions(&mockFunctionProvider{functions: []string{"gitlog", "gitb", "gita"}})
	result, _ := c.Complete(context.Background(), "git", len("git"))
	var got []string
	for _, item := range result.Items {
		got = append(got, item.Value)
	}
	if want := []string{"gita", "gitb", "gitlog"}; !slices.Equal(got, want) {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

// The executable limit counts PATH executables only: the functions and
// builtins listed before them do not crowd them out, and the menu says how
// many it left out.
func TestExecutableCompleter_LimitCountsExecutablesAfterTheFunctions(t *testing.T) {
	var functions, exes []string
	for i := range 60 {
		functions = append(functions, fmt.Sprintf("tool-fn-%02d", i))
	}
	for i := range 80 {
		exes = append(exes, fmt.Sprintf("tool-%02d", i))
	}
	exe := NewExecutableCompleter()
	exe.scanPath = func(string) []string { return exes }
	exe.SetFunctions(&mockFunctionProvider{functions: functions})
	result, _ := exe.Complete(context.Background(), "tool-", len("tool-"))
	listed := 0
	for _, item := range result.Items {
		if item.Description != "function" {
			listed++
		}
	}
	if listed != executableItemLimit || result.Hidden() != (Hidden{More: 80 - executableItemLimit}) {
		t.Errorf("Complete(tool-) = %d executables, hidden %+v, want %d and %d more", listed, result.Hidden(), executableItemLimit, 80-executableItemLimit)
	}
}

// The executables offered in command position stop at a limit, saying
// more were not shown.
func TestExecutableCompleter_SaysMoreNotShownPastItsLimit(t *testing.T) {
	var many []string
	for i := range 80 {
		many = append(many, fmt.Sprintf("tool-%02d", i))
	}
	exe := NewExecutableCompleter()
	exe.scanPath = func(string) []string { return many }
	exe.SetFunctions(&mockFunctionProvider{})
	for line, more := range map[string]bool{"tool-": true, "tool-1": false} {
		result, _ := exe.Complete(context.Background(), line, len(line))
		if result.Hidden().NotShown() != more || len(result.Items) == 0 {
			t.Errorf("Complete(%q) = %d items, hidden %+v, want more %v", line, len(result.Items), result.Hidden(), more)
		}
	}
}
