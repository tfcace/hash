package completion

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// waitUntilCanceled makes a lookup block for as long as the caller's context
// allows, for completers that have nothing better to offer while they wait.
const waitUntilCanceled time.Duration = -1

// flightPolicy says how one kind of completion data is fetched and kept. A
// fetch keeps the policy it was launched with: how long its result stays
// fresh, and when it times out, are fixed by the fetch that produced it.
type flightPolicy struct {
	// ttl is how long a successful fetch stays fresh. Zero still hands the
	// result to everyone waiting on that fetch, but never serves it again.
	ttl time.Duration
	// staleGrace is how long past ttl a value is still served, instantly,
	// while a refresh runs in the background.
	staleGrace time.Duration
	// staleWaits makes a stale lookup first wait (as a cold one would) for
	// its refresh, serving the stale value only if the refresh is still
	// running then, or hung: the old value stands in for a slow source,
	// never for a quick answer. A quick failure is that answer for every
	// lookup during its failureTTL backoff too.
	staleWaits bool
	// failureTTL is how long a failed fetch is remembered. Lookups during
	// that window report failure without relaunching the fetch. Zero retries
	// on the next lookup.
	failureTTL time.Duration
	// timeout bounds one fetch. Fetches run detached from the callers'
	// contexts, so an impatient completion deadline cannot kill a slow source
	// before it ever fills the cache. At timeout the fetch's context ends and
	// the fetch fails, releasing everyone waiting on it.
	timeout time.Duration
	// wait is how long a lookup with no usable value blocks on the fetch
	// before reporting pending: 0 never blocks, waitUntilCanceled blocks for
	// as long as the caller's context allows.
	wait time.Duration
}

// errFetchTimedOut is the failure of a fetch still running at its timeout.
var errFetchTimedOut = errors.New("completion: source timed out")

// lookupStatus is what a cache lookup found.
type lookupStatus int

const (
	lookupFresh   lookupStatus = iota // value is current
	lookupStale                       // value is past its ttl: served while a refresh runs, is hung, or backs off after failing
	lookupPending                     // no usable value yet; a fetch is running
	lookupFailed                      // no usable value; the last fetch failed recently
)

func (s lookupStatus) String() string {
	switch s {
	case lookupFresh:
		return "fresh"
	case lookupStale:
		return "stale"
	case lookupPending:
		return "pending"
	case lookupFailed:
		return "failed"
	default:
		return fmt.Sprintf("lookupStatus(%d)", int(s))
	}
}

// entryState is the phase of one cache key's data. A key is in exactly one
// of these; whether a fetch is also running within its timeout is the
// entry's flight.
type entryState int

const (
	entryCold        entryState = iota // nothing usable and nothing remembered: fetch
	entryFresh                         // a value within its ttl: serve it
	entryStale                         // a value past its ttl, within the grace: serve it and refresh
	entryFailed                        // the last fetch failed, backoff running, nothing usable: report failure
	entryFailedStale                   // the last fetch failed, backoff running, a value within its grace: serve it, do not refresh
	// entryHung: the key's fetch passed its timeout and has not returned,
	// nothing usable: report failure and launch nothing, since a second
	// fetch would hang on the same source.
	entryHung
	// entryHungStale: the key's fetch is hung, and a value is within its
	// grace: serve it. A hung source is a slow one, so the old value
	// stands in (with staleWaits too) until its grace ends.
	entryHungStale
)

// flightCache is a keyed single-flight cache for completion data produced by
// slow work (subprocesses, directory scans). Each key has at most one fetch
// at a time, ever: every lookup that needs it joins that fetch, and a fetch
// that ignores its context (a directory read on a dead mount) keeps the
// key's slot until it returns, so a hung source never piles up goroutines.
// Fetches run on their own context, so a joiner never receives another
// caller's cancellation.
// Hung fetches (past their timeout, not returned) each hold a goroutine,
// and an OS thread blocked in a syscall, until they return, and a dead
// mount hangs a read for every distinct path typed under it. So a key whose
// fetch would join maxHungFlights hung ones near it (see flightCache.near)
// launches nothing, and past maxHungFlightsTotal hung in one cache, no key
// does.
const (
	maxHungFlights      = 8
	maxHungFlightsTotal = 64
)

type flightCache[V any] struct {
	// now is the clock (nil = time.Now). Set before first use.
	now func() time.Time
	// ready, when set, is signaled whenever a fetch lands or times out, so a
	// UI showing "fetching" can retry without another keystroke.
	ready *readyNotifier
	// near reports whether a fetch for key would likely hang like the hung
	// fetch for hung (the same dead mount). nil: every key is near every
	// other. Set before first use.
	near func(key, hung string) bool

	mu      sync.Mutex
	entries map[string]*flightEntry[V]
	hung    map[string]bool // keys whose fetch is past its timeout and has not returned
}

type flightEntry[V any] struct {
	value      V
	hasValue   bool
	freshUntil time.Time
	staleUntil time.Time
	retryAt    time.Time  // failure backoff end; zero when the last fetch succeeded
	flight     *flight[V] // the key's one fetch; nil when none runs
}

// flightPhase is where a running fetch is.
type flightPhase int

const (
	// flightRunning: within its timeout. Lookups wait on it.
	flightRunning flightPhase = iota
	// flightTimedOut: past its timeout and its fetch has not returned.
	// Waiters were released with errFetchTimedOut and the entry is hung
	// (entryHung, entryHungStale): the flight keeps the key's slot so no
	// second fetch joins the hung one. Its late answer is committed when it
	// comes, a late failure backs off like any other.
	flightTimedOut
)

// flight is one fetch shared by everyone who joined it.
type flight[V any] struct {
	policy flightPolicy
	phase  flightPhase   // guarded by the cache's mu
	done   chan struct{} // closed once the outcome is known: returned or timed out
	value  V             // set before done is closed
	err    error
}

func (c *flightCache[V]) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (e *flightEntry[V]) state(now time.Time) entryState {
	usable := e.hasValue && now.Before(e.staleUntil)
	hung := e.flight != nil && e.flight.phase == flightTimedOut
	backingOff := now.Before(e.retryAt)
	switch {
	case hung && usable:
		return entryHungStale
	case hung:
		return entryHung
	case e.hasValue && now.Before(e.freshUntil):
		return entryFresh
	case backingOff && usable:
		return entryFailedStale
	case backingOff:
		return entryFailed
	case usable:
		return entryStale
	default:
		return entryCold
	}
}

// state is the phase of key's entry e in this cache: a key that would
// launch a fetch next to too many hung ones (hungCapped) is hung too,
// serving its old value if it has one. Callers hold c.mu.
func (c *flightCache[V]) state(key string, e *flightEntry[V], now time.Time) entryState {
	st := e.state(now)
	if e.flight != nil || !c.hungCapped(key) {
		return st
	}
	switch st {
	case entryCold:
		return entryHung
	case entryStale:
		return entryHungStale
	}
	return st
}

// hungCapped reports whether a fetch for key would join maxHungFlights
// hung fetches near it, or maxHungFlightsTotal in all. Callers hold c.mu.
func (c *flightCache[V]) hungCapped(key string) bool {
	if len(c.hung) >= maxHungFlightsTotal {
		return true
	}
	if c.near == nil {
		return len(c.hung) >= maxHungFlights
	}
	n := 0
	for hung := range c.hung {
		if c.near(key, hung) {
			n++
		}
	}
	return n >= maxHungFlights
}

// get returns the value for key, fetching it when there is nothing fresh.
// A stale value is returned at once while a refresh runs or is hung (after
// waiting for that refresh, with policy.staleWaits). With no usable value,
// get waits up to policy.wait (and the caller's context) for the fetch; when
// that runs out it reports lookupPending and the fetch keeps going in the
// background.
func (c *flightCache[V]) get(
	ctx context.Context,
	key string,
	policy flightPolicy,
	fetch func(context.Context) (V, error),
) (V, lookupStatus) {
	var zero V
	now := c.clock()

	c.mu.Lock()
	e := c.entry(key)
	switch c.state(key, e, now) {
	case entryFresh:
		value := e.value
		c.mu.Unlock()
		return value, lookupFresh
	case entryStale:
		f := c.startFlight(key, e, policy, fetch)
		value := e.value
		c.mu.Unlock()
		if policy.staleWaits {
			// A refresh that times out meanwhile is slow, not an answer.
			if refreshed, status := f.wait(ctx, policy.wait); status != lookupPending && f.err != errFetchTimedOut {
				return refreshed, status
			}
		}
		return value, lookupStale
	case entryFailedStale:
		if policy.staleWaits {
			// The old value stands in for a slow source only: a quick
			// failure was the answer, and stays it through the backoff.
			c.mu.Unlock()
			return zero, lookupFailed
		}
		fallthrough
	case entryHungStale:
		// An old answer beats none while the source recovers.
		value := e.value
		c.mu.Unlock()
		return value, lookupStale
	case entryFailed, entryHung:
		c.mu.Unlock()
		return zero, lookupFailed
	}
	f := c.startFlight(key, e, policy, fetch)
	c.mu.Unlock()

	return f.wait(ctx, policy.wait)
}

// warm starts a fetch for key unless its value is fresh, its last fetch
// failed recently, or a fetch is already running. It never blocks.
func (c *flightCache[V]) warm(key string, policy flightPolicy, fetch func(context.Context) (V, error)) {
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entry(key)
	switch c.state(key, e, now) {
	case entryCold, entryStale:
		c.startFlight(key, e, policy, fetch)
	}
}

// entry returns the entry for key, or a new empty one that only
// startFlight adds to the cache. Callers hold c.mu.
func (c *flightCache[V]) entry(key string) *flightEntry[V] {
	if e, ok := c.entries[key]; ok {
		return e
	}
	return &flightEntry[V]{}
}

// startFlight returns the key's running fetch, launching one when none
// runs. Callers hold c.mu and have checked that the key is not hung.
func (c *flightCache[V]) startFlight(
	key string,
	e *flightEntry[V],
	policy flightPolicy,
	fetch func(context.Context) (V, error),
) *flight[V] {
	if e.flight != nil {
		return e.flight
	}
	f := &flight[V]{policy: policy, done: make(chan struct{})}
	e.flight = f
	if c.entries == nil {
		c.entries = make(map[string]*flightEntry[V])
	}
	c.entries[key] = e
	go c.run(key, f, fetch)
	return f
}

func (c *flightCache[V]) run(key string, f *flight[V], fetch func(context.Context) (V, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), f.policy.timeout)
	defer cancel()
	// The fetch may ignore its context: the timer ends the flight for its
	// waiters either way.
	timer := time.AfterFunc(f.policy.timeout, func() { c.timeOut(key, f) })
	value, err := runFetchGuarded(ctx, fetch)
	timer.Stop()
	c.finish(key, f, value, err)
}

// timeOut fails f for its waiters if its fetch is still running.
func (c *flightCache[V]) timeOut(key string, f *flight[V]) {
	c.mu.Lock()
	e, ok := c.entries[key]
	if !ok || e.flight != f || f.phase != flightRunning {
		c.mu.Unlock()
		return // it returned first
	}
	f.phase = flightTimedOut // the entry is hung until the fetch returns
	f.err = errFetchTimedOut
	if c.hung == nil {
		c.hung = make(map[string]bool)
	}
	c.hung[key] = true
	close(f.done)
	c.mu.Unlock()
	c.ready.notify()
}

// finish records the fetch's outcome and frees the key's slot. After a
// timeout the waiters are gone, but a late value is still news; a late
// failure is not.
func (c *flightCache[V]) finish(key string, f *flight[V], value V, err error) {
	now := c.clock()
	c.mu.Lock()
	// The entry is still there: evictIdle never drops one with a flight.
	e := c.entries[key]
	e.flight = nil
	if err != nil {
		e.retryAt = now.Add(f.policy.failureTTL)
	} else {
		e.value, e.hasValue = value, true
		e.freshUntil = now.Add(f.policy.ttl)
		e.staleUntil = e.freshUntil.Add(f.policy.staleGrace)
		e.retryAt = time.Time{}
	}
	news := true
	if f.phase == flightRunning {
		f.value, f.err = value, err
		close(f.done)
	} else {
		delete(c.hung, key)
		news = err == nil
	}
	c.evictIdle(now)
	c.mu.Unlock()
	if news {
		c.ready.notify()
	}
}

// evictIdle drops entries that hold no usable value, no backoff and no
// running fetch, so caches keyed by user input do not grow without bound.
// Callers hold c.mu.
func (c *flightCache[V]) evictIdle(now time.Time) {
	for key, e := range c.entries {
		if e.flight == nil && e.state(now) == entryCold {
			delete(c.entries, key)
		}
	}
}

func (f *flight[V]) wait(ctx context.Context, wait time.Duration) (V, lookupStatus) {
	var zero V
	var timeout <-chan time.Time
	switch {
	case wait == 0:
		select {
		case <-f.done:
			return f.result()
		default:
			return zero, lookupPending
		}
	case wait > 0:
		timer := time.NewTimer(wait)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case <-f.done:
		return f.result()
	case <-timeout:
		return zero, lookupPending
	case <-ctx.Done():
		return zero, lookupPending
	}
}

func (f *flight[V]) result() (V, lookupStatus) {
	if f.err != nil {
		var zero V
		return zero, lookupFailed
	}
	return f.value, lookupFresh
}

// runFetchGuarded runs fetch, converting a panic into an ordinary failure.
// Fetches run on detached goroutines inside the user's login shell: an
// unrecovered panic there would not just break completion, it would kill the
// shell and close their terminal tab.
func runFetchGuarded[V any](ctx context.Context, fetch func(context.Context) (V, error)) (value V, err error) {
	defer func() {
		if r := recover(); r != nil {
			var zero V
			value, err = zero, fmt.Errorf("completion: source panicked: %v", r)
		}
	}()
	return fetch(ctx)
}

// readyNotifier carries the "background completion data landed" signal to
// the UI. The zero value and a nil pointer are both valid and do nothing.
type readyNotifier struct {
	mu sync.RWMutex
	fn func()
}

func (n *readyNotifier) set(fn func()) {
	n.mu.Lock()
	n.fn = fn
	n.mu.Unlock()
}

func (n *readyNotifier) notify() {
	if n == nil {
		return
	}
	n.mu.RLock()
	fn := n.fn
	n.mu.RUnlock()
	if fn != nil {
		fn()
	}
}
