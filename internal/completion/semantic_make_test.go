package completion

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestMakeHandler_Targets(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"build: src/*.go",
				"test: build",
				"clean:",
				".PHONY: build test clean",
			}, nil
		},
	}

	targets := h.parseFile("")
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d: %v", len(targets), targets)
	}
}

func TestMakeHandler_SkipsSpecialTargets(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"build:",
				".DEFAULT_GOAL:",
				".PHONY: build",
				".SUFFIXES:",
			}, nil
		},
	}

	targets := h.parseFile("")
	if len(targets) != 1 {
		t.Fatalf("expected 1 target (special skipped), got %d: %v", len(targets), targets)
	}
	if targets[0] != "build" {
		t.Errorf("expected build, got %q", targets[0])
	}
}

func TestMakeHandler_PrefixFilter(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"build:",
				"build-docker:",
				"test:",
			}, nil
		},
	}

	// Since parseTargets looks for files in cwd, we test parseFile directly
	targets := h.parseFile("")
	filtered := prefixFilterItems(targets, "build")
	if len(filtered.Items) != 2 {
		t.Fatalf("expected 2 items matching 'build', got %d", len(filtered.Items))
	}
}

func TestMakeHandler_NoMatch(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{"build:", "test:"}, nil
		},
	}

	targets := h.parseFile("")
	filtered := prefixFilterItems(targets, "deploy")
	if len(filtered.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(filtered.Items))
	}
}

func TestMakeHandler_EmptyFile(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return nil, nil
		},
	}

	targets := h.parseFile("")
	if len(targets) != 0 {
		t.Fatalf("expected 0 targets, got %d", len(targets))
	}
}

func TestMakeHandler_Deduplicates(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"build: src/a.go",
				"build: src/b.go", // duplicate target
				"test:",
			}, nil
		},
	}

	targets := h.parseFile("")
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets (deduped), got %d: %v", len(targets), targets)
	}
}

func TestMakeHandler_CachesTargets(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "Makefile"), []byte("build:\ntest:\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	calls := 0
	h := &MakeHandler{
		cacheTTL: time.Minute,
		readFile: func(path string) ([]string, error) {
			calls++
			return []string{"build:", "test:"}, nil
		},
	}

	first := h.Complete(context.Background(), nil, "b")
	second := h.Complete(context.Background(), nil, "t")

	if calls != 1 {
		t.Fatalf("expected one Makefile read across repeated completions, got %d", calls)
	}
	if len(first.Items) != 1 || first.Items[0].Value != "build" {
		t.Fatalf("unexpected first result: %#v", first.Items)
	}
	if len(second.Items) != 1 || second.Items[0].Value != "test" {
		t.Fatalf("unexpected cached second result: %#v", second.Items)
	}
}

func TestMakeHandler_TriesReadFileWithoutPreStat(t *testing.T) {
	tmpDir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	calls := 0
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			calls++
			if path != "Makefile" {
				t.Fatalf("readFile path = %q, want Makefile", path)
			}
			return []string{"build:"}, nil
		},
	}

	targets := h.parseTargets(context.Background())
	if calls != 1 {
		t.Fatalf("expected readFile to be called once without pre-stat, got %d", calls)
	}
	if len(targets) != 1 || targets[0] != "build" {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestMakeHandler_ReturnsWhenReadFileBlocks(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			<-release
			return []string{"build:"}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := h.Complete(ctx, nil, "")
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("Make completion took %s after context cancellation, want under 100ms", elapsed)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no items after context cancellation, got %#v", result.Items)
	}
}

func TestMakeHandler_CoalescesBlockedReadFile(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			calls.Add(1)
			<-release
			return []string{"build:"}, nil
		},
	}

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_ = h.Complete(ctx, nil, "")
		cancel()
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one in-flight Makefile read, got %d", got)
	}
}

func TestMakeHandler_DoesNotCoalesceBlockedReadsAcrossDirectories(t *testing.T) {
	root := t.TempDir()
	dir1 := filepath.Join(root, "one")
	dir2 := filepath.Join(root, "two")
	if err := os.Mkdir(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir2, 0o755); err != nil {
		t.Fatal(err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 2)
	var calls atomic.Int32
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			calls.Add(1)
			started <- struct{}{}
			<-release
			return []string{"build:"}, nil
		},
	}

	if err := os.Chdir(dir1); err != nil {
		t.Fatal(err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		_ = h.Complete(ctx1, nil, "")
	}()
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		cancel1()
		t.Fatal("first Makefile read did not start")
	}
	cancel1()
	<-done1

	if err := os.Chdir(dir2); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		_ = h.Complete(ctx2, nil, "")
	}()
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		cancel2()
		t.Fatal("second directory should start a separate Makefile read")
	}
	cancel2()
	<-done2

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected separate in-flight Makefile reads across directories, got %d", got)
	}
}

func TestMakeHandler_DoesNotCacheCanceledRead(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	var first atomic.Bool
	first.Store(true)
	h := &MakeHandler{
		cacheTTL: time.Minute,
		readFile: func(path string) ([]string, error) {
			if first.Swap(false) {
				<-release
				close(finished)
			}
			return []string{"build:"}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	result := h.Complete(ctx, nil, "")
	cancel()
	if len(result.Items) != 0 {
		t.Fatalf("expected no items after timeout, got %#v", result.Items)
	}

	close(release)
	<-finished

	result = h.Complete(context.Background(), nil, "")
	if len(result.Items) != 1 || result.Items[0].Value != "build" {
		t.Fatalf("canceled read should not cache an empty result, got %#v", result.Items)
	}
}
