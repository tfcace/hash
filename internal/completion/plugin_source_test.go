package completion

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A second Tab while a slow source (up to its 500ms timeout) is still
// running must join it, not launch a duplicate subprocess.
func TestPluginCompleter_SlowSourceIsNotDuplicatedWithinItsTimeout(t *testing.T) {
	var calls atomic.Int32
	c := newTestPluginCompleter(t, testPluginSpec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		calls.Add(1)
		time.Sleep(250 * time.Millisecond)
		return []string{"abc\tweb"}, nil
	})

	line := "docker rm "
	_, _ = c.Complete(context.Background(), line, len(line))
	time.Sleep(100 * time.Millisecond)
	_, _ = c.Complete(context.Background(), line, len(line))
	if got := calls.Load(); got != 1 {
		t.Fatalf("source ran %d times within its timeout, want 1", got)
	}
}

// A source call that outlives its timeout fails for the Tab waiting on it
// and is not doubled by the next Tab; when it finally returns, its result
// answers.
func TestPluginCompleter_SourcePastItsTimeoutIsNotDuplicatedAndItsLateResultAnswers(t *testing.T) {
	spec := strings.Replace(testPluginSpec, `cache_ttl = "1s"`, "cache_ttl = \"1m\"\ntimeout = \"100ms\"", 1)
	var calls atomic.Int32
	c := newTestPluginCompleter(t, spec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		calls.Add(1)
		time.Sleep(300 * time.Millisecond) // ignores its timeout
		return []string{"late\tslow"}, nil
	})

	line := "docker rm "
	_, _ = c.Complete(context.Background(), line, len(line))
	time.Sleep(150 * time.Millisecond)
	if result, _ := c.Complete(context.Background(), line, len(line)); result.Outcome() == OutcomePending {
		t.Fatal("Tab after the source timed out is still pending")
	}
	time.Sleep(250 * time.Millisecond) // the stuck call returns

	result, _ := c.Complete(context.Background(), line, len(line))
	if len(result.Items) != 1 || result.Items[0].Value != "late" {
		t.Fatalf("items = %#v, want the late result", result.Items)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("source ran %d times, want 1", got)
	}
}

// Prefetch must respect the failure backoff, or a failing source is
// relaunched on every typed space.
func TestPluginCompleter_PrefetchRespectsFailureBackoff(t *testing.T) {
	var calls atomic.Int32
	c := newTestPluginCompleter(t, testPluginSpec, func(ctx context.Context, argv []string, dir string, timeout time.Duration) ([]string, error) {
		calls.Add(1)
		return nil, errors.New("daemon down")
	})

	line := "docker rm "
	_, _ = c.Complete(context.Background(), line, len(line))
	if !eventuallyTrue(time.Second, func() bool { return calls.Load() == 1 }) {
		t.Fatal("source never ran")
	}
	time.Sleep(20 * time.Millisecond)
	for range 3 {
		c.Prefetch(line, len(line))
	}
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("failing source ran %d times, want 1 (backoff ignored by Prefetch)", got)
	}
}
