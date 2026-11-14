package completion

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// gatedCompleter ignores its context and blocks every call until released,
// tracking how many calls run at once.
type gatedCompleter struct {
	release chan struct{}
	calls   atomic.Int32
	running atomic.Int32
	maxRun  atomic.Int32
}

func newGatedCompleter() *gatedCompleter { return &gatedCompleter{release: make(chan struct{})} }

func (g *gatedCompleter) Name() string { return "gated" }

func (g *gatedCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	g.calls.Add(1)
	n := g.running.Add(1)
	for {
		m := g.maxRun.Load()
		if n <= m || g.maxRun.CompareAndSwap(m, n) {
			break
		}
	}
	defer g.running.Add(-1)
	<-g.release
	return Result{Items: []Item{{Value: "late-result"}}}, nil
}

func completeWithin(router *Router, d time.Duration, line string) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return router.CompleteBounded(ctx, line, len(line))
}

// A completer that ignores its context must not be relaunched on every Tab
// once its call outlives the old 75ms staleness gate: that piled up one
// goroutine per keystroke.
func TestRouter_StuckCompleterIsNotRelaunchedPastEditorDeadline(t *testing.T) {
	gated := newGatedCompleter()
	defer close(gated.release)
	router := NewRouter()
	router.Register(gated, PriorityFilesystem)

	for _, line := range []string{"cat ", "cat ", "cat x", "cat y"} {
		_, _ = completeWithin(router, 10*time.Millisecond, line)
		time.Sleep(90 * time.Millisecond)
	}
	if got := gated.calls.Load(); got != 1 {
		t.Fatalf("stuck completer launched %d times, want 1", got)
	}
}

// A completer runs one call at a time: while its stuck call runs, Tabs
// skip it, and once the call returns the next Tab launches one again.
func TestRouter_CompleterRunsOneCallAtATime(t *testing.T) {
	gated := newGatedCompleter()
	router := NewRouter()
	router.Register(gated, PriorityFilesystem)

	_, _ = completeWithin(router, 10*time.Millisecond, "a ")
	time.Sleep(90 * time.Millisecond)
	_, _ = completeWithin(router, 10*time.Millisecond, "b ")

	// Let exactly one blocked call finish, then ask again.
	gated.release <- struct{}{}
	time.Sleep(10 * time.Millisecond)
	_, _ = completeWithin(router, 10*time.Millisecond, "c ")
	close(gated.release)

	if got := gated.maxRun.Load(); got != 1 {
		t.Fatalf("completer ran %d calls at once, want 1", got)
	}
}

// A completer still busy with a call whose caller gave up is skipped, not
// reported pending, for other input and the same input alike: it has not
// claimed this input, nothing would wake a pending menu, and a stuck
// completer must not block every line below it.
func TestRouter_BusyCompleterLetsOthersAnswer(t *testing.T) {
	gated := newGatedCompleter()
	defer close(gated.release)
	files := &MockCompleter{name: "file", items: []Item{{Value: "file.txt"}}}
	router := NewRouter()
	router.Register(gated, PriorityToolNative)
	router.Register(files, PriorityFilesystem)

	_, _ = completeWithin(router, 10*time.Millisecond, "kubectl get ")
	for _, line := range []string{"cat ", "kubectl get "} {
		result, _ := completeWithin(router, 50*time.Millisecond, line)
		if result.Outcome() != OutcomeAnswer || result.Items[0].Value != "file.txt" {
			t.Fatalf("Tab on %q = %v %#v, want the file answer", line, result.Outcome(), result.Items)
		}
	}
}

// A completer that ignores its context keeps its call running long past the
// deadline it was launched with. Waiting on it on every Tab for the same
// input timed every Tab out and never let filenames answer: it is skipped,
// at once, and lower completers answer.
func TestRouter_CallIgnoringItsDeadlineIsSkippedForLowerCompleters(t *testing.T) {
	gated := newGatedCompleter()
	defer close(gated.release)
	files := &MockCompleter{name: "file", items: []Item{{Value: "file.txt"}}}
	router := NewRouter()
	router.Register(gated, PriorityToolNative)
	router.Register(files, PriorityFilesystem)

	_, _ = completeWithin(router, 10*time.Millisecond, "kubectl get ")
	for i := range 3 {
		start := time.Now()
		result, err := completeWithin(router, 150*time.Millisecond, "kubectl get ")
		if err != nil || result.Outcome() != OutcomeAnswer || result.Items[0].Value != "file.txt" {
			t.Fatalf("Tab %d with the call stuck = %v %#v (err %v), want the file answer", i, result.Outcome(), result.Items, err)
		}
		if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
			t.Fatalf("Tab %d took %s: a stuck call is skipped, not waited on", i, elapsed)
		}
	}
	if got := gated.calls.Load(); got != 1 {
		t.Fatalf("stuck completer launched %d times, want 1", got)
	}
}

type completerFunc func(ctx context.Context, line string, pos int) (Result, error)

func (f completerFunc) Name() string { return "func" }
func (f completerFunc) Complete(ctx context.Context, line string, pos int) (Result, error) {
	return f(ctx, line, pos)
}

// A stuck prefetcher must not be relaunched, and its eventual return must not
// let two run at once.
func TestRouter_StuckPrefetcherIsNotRelaunched(t *testing.T) {
	release := make(chan struct{})
	blocking := &blockingPrefetchCompleter{release: release}
	router := NewRouter()
	router.Register(blocking, PriorityToolNative)

	router.PrefetchBounded("kubectl ", len("kubectl "))
	time.Sleep(90 * time.Millisecond)
	router.PrefetchBounded("kubectl get ", len("kubectl get "))
	time.Sleep(10 * time.Millisecond)
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("stuck prefetcher launched %d times, want 1", got)
	}
	close(release)
	if !eventuallyTrue(time.Second, func() bool {
		router.PrefetchBounded("kubectl get pods ", len("kubectl get pods "))
		return blocking.calls.Load() == 2
	}) {
		t.Fatalf("prefetcher not relaunched after it returned, calls = %d", blocking.calls.Load())
	}
}

// The router's callback is the one source of truth: replacing it reaches
// completers registered earlier.
func TestRouter_SetOnReadyReplacesTheCallbackForEveryCompleter(t *testing.T) {
	router := NewRouter()
	exe := NewExecutableCompleter()
	exe.scanPath = func(string) []string { return []string{"tool"} }
	router.Register(exe, PriorityExecutable)
	var first, second atomic.Int32
	router.SetOnReady(func() { first.Add(1) })
	router.SetOnReady(func() { second.Add(1) })

	_, _ = exe.Complete(context.Background(), "to", 2)
	if !eventuallyTrue(time.Second, func() bool { return second.Load() == 1 }) || first.Load() != 0 {
		t.Fatalf("notifications: first = %d, second = %d; want 0, 1", first.Load(), second.Load())
	}
}

// Every completer that can report pending is wired to the router's ready
// callback, whether registered before or after SetOnReady.
func TestRouter_SetOnReadyReachesBackgroundCompleters(t *testing.T) {
	router := NewRouter()
	vcs := NewVCSCompleter()
	release := make(chan struct{})
	vcs.listGitRefs = func(ctx context.Context, dir string) ([]string, error) {
		<-release
		return []string{"main"}, nil
	}
	router.Register(vcs, PriorityVCS)
	var ready atomic.Int32
	router.SetOnReady(func() { ready.Add(1) })
	exe := NewExecutableCompleter()
	router.Register(exe, PriorityExecutable)
	sem := NewSemanticCompleter()
	router.Register(sem, PrioritySemantic)

	result, _ := completeWithin(router, 150*time.Millisecond, "git checkout ")
	if result.Outcome() != OutcomePending {
		t.Fatalf("outcome = %v, want pending", result.Outcome())
	}
	close(release)
	if !eventuallyTrue(time.Second, func() bool { return ready.Load() >= 1 }) {
		t.Fatal("router ready callback never fired for the vcs source")
	}
	for name, n := range map[string]*readyNotifier{"executable": &exe.ready, "semantic": &sem.ready} {
		before := ready.Load()
		n.notify()
		if ready.Load() != before+1 {
			t.Fatalf("%s completer registered after SetOnReady does not reach the router callback", name)
		}
	}
}

// A completer bug must not take the shell down: Complete runs on a router
// goroutine, where an unrecovered panic kills the process.
func TestRouter_PanickingCompleterFallsThrough(t *testing.T) {
	boom := completerFunc(func(ctx context.Context, line string, pos int) (Result, error) {
		panic("completer bug")
	})
	files := &MockCompleter{name: "file", items: []Item{{Value: "file.txt"}}}
	router := NewRouter()
	router.Register(boom, PriorityToolNative)
	router.Register(files, PriorityFilesystem)

	result, err := completeWithin(router, time.Second, "cat ")
	if err != nil || len(result.Items) != 1 || result.Items[0].Value != "file.txt" {
		t.Fatalf("got (%#v, %v), want the fallback answer", result.Items, err)
	}
	// The panicked call released its slot.
	if _, err := completeWithin(router, time.Second, "cat "); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

type panickingPrefetcher struct{ calls atomic.Int32 }

func (p *panickingPrefetcher) Name() string { return "panicking-prefetch" }
func (p *panickingPrefetcher) Complete(ctx context.Context, line string, pos int) (Result, error) {
	return Result{}, nil
}
func (p *panickingPrefetcher) Prefetch(line string, pos int) {
	p.calls.Add(1)
	panic("prefetch bug")
}

func TestRouter_PanickingPrefetcherDoesNotCrash(t *testing.T) {
	p := &panickingPrefetcher{}
	router := NewRouter()
	router.Register(p, PriorityToolNative)
	router.PrefetchBounded("x ", 2)
	if !eventuallyTrue(time.Second, func() bool {
		router.PrefetchBounded("x ", 2)
		return p.calls.Load() >= 2
	}) {
		t.Fatal("prefetcher slot not released after a panic")
	}
}

// A call that has returned frees its completer for the next lookup at once:
// back-to-back lookups (auto-drill, relisting after an accept) each get the
// completer's answer instead of finding it still marked busy.
func TestRouter_BackToBackLookupsReachAReturnedCompleter(t *testing.T) {
	router := NewRouter()
	router.Register(completerFunc(func(context.Context, string, int) (Result, error) {
		return Result{Items: []Item{{Value: "x"}}}, nil
	}), PriorityFilesystem)
	misses := 0
	for i := 0; i < 20000; i++ {
		if res, _ := router.Complete(context.Background(), "cat ", 4); len(res.Items) == 0 {
			misses++
		}
	}
	if misses > 0 {
		t.Fatalf("%d of 20000 lookups skipped a completer whose last call had returned", misses)
	}
}

// A replacement start past the cursor is a completer bug; the router slices
// the line at it outside the completer's panic guard, so it keeps the start
// at the cursor instead of crashing the shell.
func TestRouter_ReplacementStartPastTheCursorStaysAtTheCursor(t *testing.T) {
	for _, from := range []int{9, 100} {
		router := NewRouter()
		router.Register(completerFunc(func(context.Context, string, int) (Result, error) {
			return Result{Items: []Item{{Value: "x"}}, ReplaceFrom: from}, nil
		}), PriorityFilesystem)
		line := "cat abc def"
		result, err := router.Complete(context.Background(), line, 7)
		if err != nil || len(result.Items) != 1 || result.ReplaceFrom != 7 {
			t.Errorf("from %d: got (%#v from %d, %v), want the item replacing from the cursor", from, result.Items, result.ReplaceFrom, err)
		}
	}
}
