package completion

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a settable clock for cache tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// gatedFetch is a fetch that blocks until released and counts its calls.
type gatedFetch struct {
	calls   atomic.Int32
	release chan struct{}
	value   string
	err     error
}

func newGatedFetch(value string) *gatedFetch {
	return &gatedFetch{release: make(chan struct{}), value: value}
}

func (g *gatedFetch) fetch(ctx context.Context) (string, error) {
	g.calls.Add(1)
	<-g.release
	return g.value, g.err
}

var testFlightPolicy = flightPolicy{
	ttl:        time.Second,
	failureTTL: time.Second,
	timeout:    time.Minute,
	wait:       0,
}

// waitCommitted blocks until no fetch is running for key.
func waitCommitted[V any](t *testing.T, c *flightCache[V], key string) {
	t.Helper()
	if !eventuallyTrue(time.Second, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		e, ok := c.entries[key]
		return !ok || e.flight == nil
	}) {
		t.Fatalf("fetch for %q never committed", key)
	}
}

// freshValue returns the value for key if it is fresh, without starting a
// fetch.
func freshValue[V any](c *flightCache[V], key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && e.state(c.clock()) == entryFresh {
		return e.value, true
	}
	var zero V
	return zero, false
}

func TestFlightCache_ColdLookupIsPendingThenFresh(t *testing.T) {
	var c flightCache[string]
	g := newGatedFetch("v1")

	if _, st := c.get(context.Background(), "k", testFlightPolicy, g.fetch); st != lookupPending {
		t.Fatalf("cold lookup = %v, want pending", st)
	}
	close(g.release)
	waitCommitted(t, &c, "k")

	v, st := c.get(context.Background(), "k", testFlightPolicy, g.fetch)
	if st != lookupFresh || v != "v1" {
		t.Fatalf("after fetch = (%q, %v), want (v1, fresh)", v, st)
	}
	if got := g.calls.Load(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
}

func TestFlightCache_ConcurrentLookupsShareOneFetch(t *testing.T) {
	var c flightCache[string]
	g := newGatedFetch("v1")
	policy := testFlightPolicy
	policy.wait = waitUntilCanceled

	var wg sync.WaitGroup
	results := make([]string, 5)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = c.get(context.Background(), "k", policy, g.fetch)
		}(i)
	}
	if !eventuallyTrue(time.Second, func() bool { return g.calls.Load() == 1 }) {
		t.Fatal("fetch never started")
	}
	close(g.release)
	wg.Wait()

	if got := g.calls.Load(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
	for i, v := range results {
		if v != "v1" {
			t.Fatalf("waiter %d got %q, want v1", i, v)
		}
	}
}

func TestFlightCache_JoinerDoesNotInheritFirstCallersCancellation(t *testing.T) {
	var c flightCache[string]
	fetch := func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(30 * time.Millisecond):
			return "v1", nil
		}
	}
	policy := testFlightPolicy
	policy.wait = waitUntilCanceled

	first, cancel := context.WithCancel(context.Background())
	go func() { _, _ = c.get(first, "k", policy, fetch) }()
	time.Sleep(5 * time.Millisecond)
	cancel()

	v, st := c.get(context.Background(), "k", policy, fetch)
	if st != lookupFresh || v != "v1" {
		t.Fatalf("joiner got (%q, %v), want (v1, fresh)", v, st)
	}
}

func TestFlightCache_CallerDeadlineReportsPendingAndFetchStillCommits(t *testing.T) {
	var c flightCache[string]
	g := newGatedFetch("v1")
	policy := testFlightPolicy
	policy.wait = waitUntilCanceled

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, st := c.get(ctx, "k", policy, g.fetch); st != lookupPending {
		t.Fatalf("lookup past caller deadline = %v, want pending", st)
	}
	close(g.release)
	waitCommitted(t, &c, "k")
	if v, ok := freshValue(&c, "k"); !ok || v != "v1" {
		t.Fatalf("fresh value = (%q, %v), want (v1, true)", v, ok)
	}
}

func TestFlightCache_StaleValueServedWhileRefreshing(t *testing.T) {
	clock := newFakeClock()
	c := flightCache[string]{now: clock.Now}
	policy := testFlightPolicy
	policy.staleGrace = time.Minute

	g1 := newGatedFetch("old")
	close(g1.release)
	c.get(context.Background(), "k", policy, g1.fetch)
	waitCommitted(t, &c, "k")

	clock.Advance(2 * time.Second) // past ttl, within grace
	g2 := newGatedFetch("new")
	v, st := c.get(context.Background(), "k", policy, g2.fetch)
	if st != lookupStale || v != "old" {
		t.Fatalf("expired lookup = (%q, %v), want (old, stale)", v, st)
	}
	if !eventuallyTrue(time.Second, func() bool { return g2.calls.Load() == 1 }) {
		t.Fatal("stale lookup did not start a refresh")
	}
	// A second stale lookup joins the running refresh.
	c.get(context.Background(), "k", policy, g2.fetch)
	close(g2.release)
	waitCommitted(t, &c, "k")
	if got := g2.calls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if v, _ := c.get(context.Background(), "k", policy, g2.fetch); v != "new" {
		t.Fatalf("after refresh = %q, want new", v)
	}
}

func TestFlightCache_ValuePastGraceIsCold(t *testing.T) {
	clock := newFakeClock()
	c := flightCache[string]{now: clock.Now}
	policy := testFlightPolicy
	policy.staleGrace = time.Second

	g := newGatedFetch("v")
	close(g.release)
	c.get(context.Background(), "k", policy, g.fetch)
	waitCommitted(t, &c, "k")

	clock.Advance(3 * time.Second)
	if _, st := c.get(context.Background(), "k", policy, newGatedFetch("x").fetch); st != lookupPending {
		t.Fatalf("lookup past grace = %v, want pending", st)
	}
}

func TestFlightCache_FailureBacksOffWithoutRelaunching(t *testing.T) {
	clock := newFakeClock()
	c := flightCache[string]{now: clock.Now}
	g := newGatedFetch("")
	g.err = errors.New("boom")
	close(g.release)

	c.get(context.Background(), "k", testFlightPolicy, g.fetch)
	waitCommitted(t, &c, "k")

	for range 3 {
		if _, st := c.get(context.Background(), "k", testFlightPolicy, g.fetch); st != lookupFailed {
			t.Fatalf("lookup during backoff = %v, want failed", st)
		}
		c.warm("k", testFlightPolicy, g.fetch)
	}
	if got := g.calls.Load(); got != 1 {
		t.Fatalf("fetch calls during backoff = %d, want 1", got)
	}

	clock.Advance(2 * time.Second)
	c.get(context.Background(), "k", testFlightPolicy, g.fetch)
	if !eventuallyTrue(time.Second, func() bool { return g.calls.Load() == 2 }) {
		t.Fatalf("fetch was not retried after backoff, calls = %d", g.calls.Load())
	}
}

func TestFlightCache_FailureKeepsServingStaleValue(t *testing.T) {
	clock := newFakeClock()
	c := flightCache[string]{now: clock.Now}
	policy := testFlightPolicy
	policy.staleGrace = time.Minute

	ok := newGatedFetch("old")
	close(ok.release)
	c.get(context.Background(), "k", policy, ok.fetch)
	waitCommitted(t, &c, "k")

	clock.Advance(2 * time.Second)
	bad := newGatedFetch("")
	bad.err = errors.New("daemon down")
	close(bad.release)
	c.get(context.Background(), "k", policy, bad.fetch)
	waitCommitted(t, &c, "k")

	v, st := c.get(context.Background(), "k", policy, bad.fetch)
	if st != lookupStale || v != "old" {
		t.Fatalf("lookup after failed refresh = (%q, %v), want (old, stale)", v, st)
	}
	if got := bad.calls.Load(); got != 1 {
		t.Fatalf("failed source relaunched during backoff: calls = %d", got)
	}
}

func TestFlightCache_RunningFetchIsJoinedNotDuplicated(t *testing.T) {
	var c flightCache[string]
	g := newGatedFetch("v")
	c.get(context.Background(), "k", testFlightPolicy, g.fetch)
	c.get(context.Background(), "k", testFlightPolicy, g.fetch)
	c.warm("k", testFlightPolicy, g.fetch)
	close(g.release)
	waitCommitted(t, &c, "k")
	if got := g.calls.Load(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
}

func TestFlightCache_EvictsIdleEntries(t *testing.T) {
	clock := newFakeClock()
	c := flightCache[string]{now: clock.Now}
	for _, key := range []string{"a", "b", "c"} {
		g := newGatedFetch(key)
		close(g.release)
		c.get(context.Background(), key, testFlightPolicy, g.fetch)
		waitCommitted(t, &c, key)
	}
	if got := len(c.entries); got != 3 {
		t.Fatalf("size = %d, want 3", got)
	}

	clock.Advance(time.Hour)
	g := newGatedFetch("d")
	close(g.release)
	c.get(context.Background(), "d", testFlightPolicy, g.fetch)
	waitCommitted(t, &c, "d")
	if got := len(c.entries); got != 1 {
		t.Fatalf("size after expired entries = %d, want 1 (only d)", got)
	}
}

func TestFlightCache_PanickingFetchIsAFailure(t *testing.T) {
	var c flightCache[string]
	policy := testFlightPolicy
	policy.wait = waitUntilCanceled
	_, st := c.get(context.Background(), "k", policy, func(context.Context) (string, error) {
		panic("boom")
	})
	if st != lookupFailed {
		t.Fatalf("panicking fetch = %v, want failed", st)
	}
}

func TestFlightCache_NotifiesReadyOncePerFetch(t *testing.T) {
	var notified atomic.Int32
	ready := &readyNotifier{}
	ready.set(func() { notified.Add(1) })
	c := flightCache[string]{ready: ready}

	g := newGatedFetch("v")
	c.get(context.Background(), "k", testFlightPolicy, g.fetch)
	c.get(context.Background(), "k", testFlightPolicy, g.fetch) // joins
	close(g.release)
	waitCommitted(t, &c, "k")
	c.get(context.Background(), "k", testFlightPolicy, g.fetch) // fresh

	if got := notified.Load(); got != 1 {
		t.Fatalf("ready notifications = %d, want 1", got)
	}
}

func TestFlightCache_ZeroTTLHandsResultToWaitersOnly(t *testing.T) {
	var c flightCache[string]
	policy := testFlightPolicy
	policy.ttl = 0
	policy.wait = waitUntilCanceled
	var calls atomic.Int32
	fetch := func(context.Context) (string, error) { calls.Add(1); return "v", nil }

	if v, st := c.get(context.Background(), "k", policy, fetch); st != lookupFresh || v != "v" {
		t.Fatalf("first = (%q, %v)", v, st)
	}
	c.get(context.Background(), "k", policy, fetch)
	if got := calls.Load(); got != 2 {
		t.Fatalf("zero-ttl value was reused: calls = %d, want 2", got)
	}
}

func TestReadyNotifier_NilIsSafe(t *testing.T) {
	var n *readyNotifier
	n.notify()
}

// getWithin runs one lookup and fails the test if it does not return within
// limit: a lookup that blocks forever is the bug, not a hung test.
func getWithin(t *testing.T, c *flightCache[string], policy flightPolicy, fetch func(context.Context) (string, error), limit time.Duration) (string, lookupStatus) {
	t.Helper()
	type got struct {
		v  string
		st lookupStatus
	}
	done := make(chan got, 1)
	go func() {
		v, st := c.get(context.Background(), "k", policy, fetch)
		done <- got{v, st}
	}()
	select {
	case g := <-done:
		return g.v, g.st
	case <-time.After(limit):
		t.Fatalf("lookup still blocked after %s", limit)
		return "", lookupPending
	}
}

// A fetch that ignores its context (a directory read on a dead mount) must
// still end: at its timeout the flight fails, waiters are released and the
// UI is told, instead of reporting pending forever.
func TestFlightCache_FetchPastItsTimeoutFails(t *testing.T) {
	var notified atomic.Int32
	ready := &readyNotifier{}
	ready.set(func() { notified.Add(1) })
	c := flightCache[string]{ready: ready}
	hung := newGatedFetch("late")
	defer close(hung.release)
	policy := testFlightPolicy
	policy.timeout = 30 * time.Millisecond
	policy.wait = waitUntilCanceled

	if _, st := getWithin(t, &c, policy, hung.fetch, time.Second); st != lookupFailed {
		t.Fatalf("lookup on a fetch past its timeout = %v, want failed", st)
	}
	// The timeout wakes the waiter first and notifies right after, so the
	// notification may land a moment after the lookup returns.
	deadline := time.Now().Add(time.Second)
	for notified.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := notified.Load(); got != 1 {
		t.Fatalf("ready notifications = %d, want 1 (the timeout is news for a pending menu)", got)
	}
	policy.wait = 0
	if _, st := c.get(context.Background(), "k", policy, hung.fetch); st != lookupFailed {
		t.Fatalf("lookup after the timeout = %v, want failed", st)
	}
}

// A hung fetch is never joined by a second one for the same key: each
// relaunch would leak another goroutine (and OS thread) blocked in the same
// syscall. The key stays failed until the hung fetch returns.
func TestFlightCache_HungFetchIsNotRelaunched(t *testing.T) {
	var c flightCache[string]
	hung := newGatedFetch("late")
	defer close(hung.release)
	policy := testFlightPolicy
	policy.timeout = 20 * time.Millisecond
	policy.failureTTL = 5 * time.Millisecond

	for range 30 {
		_, _ = c.get(context.Background(), "k", policy, hung.fetch)
		c.warm("k", policy, hung.fetch)
		time.Sleep(10 * time.Millisecond)
	}
	if got := hung.calls.Load(); got != 1 {
		t.Fatalf("hung fetch launched %d times, want 1", got)
	}
}

// When the hung fetch finally returns, its answer is still news: it lands
// in the cache and wakes the UI.
func TestFlightCache_LateResultOfTimedOutFetchIsKept(t *testing.T) {
	var notified atomic.Int32
	ready := &readyNotifier{}
	ready.set(func() { notified.Add(1) })
	c := flightCache[string]{ready: ready}
	hung := newGatedFetch("late")
	policy := testFlightPolicy
	policy.timeout = 20 * time.Millisecond
	policy.wait = waitUntilCanceled

	if _, st := getWithin(t, &c, policy, hung.fetch, time.Second); st != lookupFailed {
		t.Fatalf("lookup past timeout = %v, want failed", st)
	}
	close(hung.release)
	waitCommitted(t, &c, "k")
	if v, st := c.get(context.Background(), "k", policy, hung.fetch); st != lookupFresh || v != "late" {
		t.Fatalf("after the late result = (%q, %v), want (late, fresh)", v, st)
	}
	if got := notified.Load(); got != 2 {
		t.Fatalf("ready notifications = %d, want 2 (timeout, then the late result)", got)
	}
	if got := hung.calls.Load(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
}

// staleSeeded returns a cache holding "old" for "k", past its ttl and within
// its grace, and the policy it was fetched with.
func staleSeeded(t *testing.T, policy flightPolicy) (*flightCache[string], *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	c := &flightCache[string]{now: clock.Now}
	seed := newGatedFetch("old")
	close(seed.release)
	c.get(context.Background(), "k", policy, seed.fetch)
	waitCommitted(t, c, "k")
	clock.Advance(policy.ttl + time.Millisecond)
	return c, clock
}

// waitHung blocks until key's fetch has passed its timeout.
func waitHung[V any](t *testing.T, c *flightCache[V], key string) {
	t.Helper()
	if !eventuallyTrue(time.Second, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		e, ok := c.entries[key]
		return ok && e.flight != nil && e.flight.phase == flightTimedOut
	}) {
		t.Fatalf("fetch for %q never timed out", key)
	}
}

// A refresh hung past its timeout (a directory read on a dead mount) is a
// slow source, not an answer: the last value keeps standing in, without a
// second fetch, until its grace ends. Then the key reports failure until the
// hung fetch returns.
func TestFlightCache_StaleValueStandsInWhileItsRefreshIsHung(t *testing.T) {
	for _, staleWaits := range []bool{false, true} {
		policy := testFlightPolicy
		policy.staleGrace = time.Minute
		policy.staleWaits = staleWaits
		policy.failureTTL = 0
		policy.timeout = 20 * time.Millisecond
		c, clock := staleSeeded(t, policy)

		hung := newGatedFetch("new")
		if v, st := c.get(context.Background(), "k", policy, hung.fetch); st != lookupStale || v != "old" {
			t.Fatalf("staleWaits=%v: lookup while refreshing = (%q, %v), want (old, stale)", staleWaits, v, st)
		}
		waitHung(t, c, "k")
		for range 3 {
			if v, st := c.get(context.Background(), "k", policy, hung.fetch); st != lookupStale || v != "old" {
				t.Fatalf("staleWaits=%v: lookup with the refresh hung = (%q, %v), want (old, stale)", staleWaits, v, st)
			}
		}
		clock.Advance(time.Hour)
		if _, st := c.get(context.Background(), "k", policy, hung.fetch); st != lookupFailed {
			t.Fatalf("staleWaits=%v: lookup past the grace with the refresh hung = %v, want failed", staleWaits, st)
		}
		if got := hung.calls.Load(); got != 1 {
			t.Fatalf("staleWaits=%v: refresh launched %d times, want 1", staleWaits, got)
		}
		close(hung.release)
		waitCommitted(t, c, "k")
		if v, st := c.get(context.Background(), "k", policy, hung.fetch); st != lookupFresh || v != "new" {
			t.Fatalf("staleWaits=%v: after the hung refresh returned = (%q, %v), want (new, fresh)", staleWaits, v, st)
		}
	}
}

// Each distinct key on a dead mount (one hung directory read per path
// typed) would hold another goroutine and OS thread forever. Past
// maxHungFlights hung fetches, a key that needs a fetch reports failure
// without launching one, and leaves no entry behind; once a hung fetch
// returns, fetching resumes.
func TestFlightCache_HungFetchesAreCapped(t *testing.T) {
	var c flightCache[string]
	hung := newGatedFetch("late")
	policy := testFlightPolicy
	policy.timeout = 10 * time.Millisecond
	policy.failureTTL = 0
	for i := range maxHungFlights {
		key := strconv.Itoa(i)
		c.get(context.Background(), key, policy, hung.fetch)
		waitHung(t, &c, key)
	}

	extra := newGatedFetch("v")
	close(extra.release)
	for range 3 {
		if _, st := c.get(context.Background(), "extra", policy, extra.fetch); st != lookupFailed {
			t.Fatalf("lookup with %d fetches hung = %v, want failed", maxHungFlights, st)
		}
		c.warm("extra", policy, extra.fetch)
	}
	if got := extra.calls.Load(); got != 0 {
		t.Fatalf("fetches launched past the cap = %d, want 0", got)
	}
	c.mu.Lock()
	entries := len(c.entries)
	c.mu.Unlock()
	if entries != maxHungFlights {
		t.Fatalf("entries = %d, want %d (no entry for a lookup that launched nothing)", entries, maxHungFlights)
	}

	close(hung.release)
	for i := range maxHungFlights {
		waitCommitted(t, &c, strconv.Itoa(i))
	}
	policy.wait = waitUntilCanceled
	if v, st := c.get(context.Background(), "extra", policy, extra.fetch); st != lookupFresh || v != "v" {
		t.Fatalf("lookup after the hung fetches returned = (%q, %v), want (v, fresh)", v, st)
	}
}

// With near set, only hung fetches near a key count toward its cap; past
// maxHungFlightsTotal hung in all, no key launches.
func TestFlightCache_HungCapCountsOnlyNearbyKeys(t *testing.T) {
	group := func(key string) string { g, _, _ := strings.Cut(key, "-"); return g }
	c := flightCache[string]{near: func(key, hung string) bool { return group(key) == group(hung) }}
	hung := newGatedFetch("late")
	defer close(hung.release)
	policy := testFlightPolicy
	policy.timeout = 10 * time.Millisecond
	policy.failureTTL = 0
	hangKey := func(key string) {
		c.get(context.Background(), key, policy, hung.fetch)
		waitHung(t, &c, key)
	}
	for i := range maxHungFlights {
		hangKey("a-" + strconv.Itoa(i))
	}
	other := newGatedFetch("v")
	close(other.release)
	policy.wait = waitUntilCanceled
	if _, st := c.get(context.Background(), "a-extra", policy, other.fetch); st != lookupFailed {
		t.Fatalf("lookup near %d hung fetches = %v, want failed", maxHungFlights, st)
	}
	if v, st := c.get(context.Background(), "b-0", policy, other.fetch); st != lookupFresh || v != "v" {
		t.Fatalf("lookup near no hung fetch = (%q, %v), want (v, fresh)", v, st)
	}

	// Fill the cache to the total cap with keys near nothing else.
	for i := maxHungFlights; i < maxHungFlightsTotal; i++ {
		hangKey("g" + strconv.Itoa(i) + "-x")
	}
	if _, st := c.get(context.Background(), "b-1", policy, other.fetch); st != lookupFailed {
		t.Fatalf("lookup with %d fetches hung in all = %v, want failed", maxHungFlightsTotal, st)
	}
	if got := other.calls.Load(); got != 1 {
		t.Fatalf("fetches launched = %d, want 1 (only the key near nothing hung)", got)
	}
}

func TestInParentDirectory(t *testing.T) {
	tests := []struct {
		path, hung string
		want       bool
	}{
		{"/dead/b", "/dead/a", true},
		{"/dead/b/c/", "/dead/a/", true},
		{"/dead", "/dead/a", true},
		{"/dead/a", "/dead/a", true},
		{"/deadline/x", "/dead/a", false},
		{"/tmp/x", "/dead/a", false},
		{"/tmp/x", "/dead", true}, // a hung mount at the top: its parent is /
	}
	for _, tt := range tests {
		if got := inParentDirectory(tt.path, tt.hung); got != tt.want {
			t.Errorf("inParentDirectory(%q, %q) = %v, want %v", tt.path, tt.hung, got, tt.want)
		}
	}
}

// With staleWaits the old value stands in for a slow source only: a refresh
// that fails quickly is the answer, on the lookup that waited for it and on
// every lookup during the backoff after it (failureTTL > 0), not the old
// value on some and the failure on others.
func TestFlightCache_StaleWaitsReportsAQuickFailureThroughItsBackoff(t *testing.T) {
	policy := testFlightPolicy
	policy.staleGrace = time.Minute
	policy.staleWaits = true
	policy.failureTTL = time.Second
	policy.wait = waitUntilCanceled
	c, clock := staleSeeded(t, policy)

	bad := newGatedFetch("")
	bad.err = errors.New("permission denied")
	close(bad.release)
	for i := range 3 {
		if v, st := c.get(context.Background(), "k", policy, bad.fetch); st != lookupFailed {
			t.Fatalf("lookup %d after a quick failed refresh = (%q, %v), want failed", i, v, st)
		}
	}
	if got := bad.calls.Load(); got != 1 {
		t.Fatalf("refresh calls during the backoff = %d, want 1", got)
	}
	clock.Advance(2 * time.Second)
	good := newGatedFetch("new")
	close(good.release)
	if v, st := c.get(context.Background(), "k", policy, good.fetch); st != lookupFresh || v != "new" {
		t.Fatalf("lookup after the backoff = (%q, %v), want (new, fresh)", v, st)
	}
}
