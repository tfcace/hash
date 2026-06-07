package completion

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNPMHandler_RunScripts(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{
				"scripts": {
					"build": "tsc",
					"test": "jest",
					"lint": "eslint ."
				}
			}`), nil
		},
	}

	result := h.Complete(context.Background(), []string{"run"}, "")
	if len(result.Items) != 3 {
		t.Fatalf("expected 3 items, got %d: %+v", len(result.Items), result.Items)
	}
}

func TestNPMHandler_PrefixFilter(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{
				"scripts": {
					"build": "tsc",
					"build:watch": "tsc -w",
					"test": "jest"
				}
			}`), nil
		},
	}

	result := h.Complete(context.Background(), []string{"run"}, "build")
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 items matching 'build', got %d", len(result.Items))
	}
}

func TestNPMHandler_LimitsLargeScriptList(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"scripts":{`)
	for i := 0; i < 5000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		name := "script-" + strconv.Itoa(i)
		b.WriteString(strconv.Quote(name))
		b.WriteByte(':')
		b.WriteString(strconv.Quote("echo " + name))
	}
	b.WriteString(`}}`)

	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(b.String()), nil
		},
	}

	result := h.Complete(context.Background(), []string{"run"}, "script-")
	if len(result.Items) > completionItemLimit {
		t.Fatalf("npm completion returned %d items, want at most %d", len(result.Items), completionItemLimit)
	}
	if len(result.Items) != completionItemLimit {
		t.Fatalf("npm completion returned %d items, want %d", len(result.Items), completionItemLimit)
	}
}

func TestNPMHandler_OnlyRunSubcommand(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{"scripts": {"build": "tsc"}}`), nil
		},
	}

	// Should not complete for non-run subcommands
	result := h.Complete(context.Background(), []string{"install"}, "build")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items for 'install', got %d", len(result.Items))
	}
}

func TestNPMHandler_NoPackageJSON(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return nil, &dummyError{msg: "no such file"}
		},
	}

	result := h.Complete(context.Background(), []string{"run"}, "")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items without package.json, got %d", len(result.Items))
	}
}

func TestNPMHandler_EmptyArgs(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{"scripts": {"build": "tsc"}}`), nil
		},
	}

	result := h.Complete(context.Background(), nil, "")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items without subcommand, got %d", len(result.Items))
	}
}

func TestNPMHandler_ScriptDescription(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{"scripts": {"build": "webpack --mode production"}}`), nil
		},
	}

	result := h.Complete(context.Background(), []string{"run"}, "")
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	if result.Items[0].Description != "webpack --mode production" {
		t.Errorf("description = %q, want script content", result.Items[0].Description)
	}
}

func TestNPMHandler_CachesPackageScripts(t *testing.T) {
	calls := 0
	h := &NPMHandler{
		cacheTTL: time.Minute,
		readFile: func(path string) ([]byte, error) {
			calls++
			return []byte(`{"scripts": {"build": "tsc", "test": "jest"}}`), nil
		},
	}

	first := h.Complete(context.Background(), []string{"run"}, "b")
	second := h.Complete(context.Background(), []string{"run"}, "t")

	if calls != 1 {
		t.Fatalf("expected one package.json read across repeated completions, got %d", calls)
	}
	if len(first.Items) != 1 || first.Items[0].Value != "build" {
		t.Fatalf("unexpected first result: %#v", first.Items)
	}
	if len(second.Items) != 1 || second.Items[0].Value != "test" {
		t.Fatalf("unexpected cached second result: %#v", second.Items)
	}
}

func TestNPMHandler_CachesMissingPackageJSON(t *testing.T) {
	calls := 0
	h := &NPMHandler{
		cacheTTL: time.Minute,
		readFile: func(path string) ([]byte, error) {
			calls++
			return nil, &dummyError{msg: "no such file"}
		},
	}

	first := h.Complete(context.Background(), []string{"run"}, "")
	second := h.Complete(context.Background(), []string{"run"}, "")

	if calls != 1 {
		t.Fatalf("expected missing package.json to be cached, got %d reads", calls)
	}
	if len(first.Items) != 0 || len(second.Items) != 0 {
		t.Fatalf("expected no completions, got first=%#v second=%#v", first.Items, second.Items)
	}
}

func TestNPMHandler_ReturnsWhenReadFileBlocks(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			<-release
			return []byte(`{"scripts": {"build": "tsc"}}`), nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := h.Complete(ctx, []string{"run"}, "")
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("NPM completion took %s after context cancellation, want under 100ms", elapsed)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no items after context cancellation, got %#v", result.Items)
	}
}

func TestNPMHandler_CoalescesBlockedReadFile(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			calls.Add(1)
			<-release
			return []byte(`{"scripts": {"build": "tsc"}}`), nil
		},
	}

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_ = h.Complete(ctx, []string{"run"}, "")
		cancel()
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one in-flight package.json read, got %d", got)
	}
}

func TestNPMHandler_DoesNotCoalesceBlockedReadsAcrossDirectories(t *testing.T) {
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
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			calls.Add(1)
			started <- struct{}{}
			<-release
			return []byte(`{"scripts": {"build": "tsc"}}`), nil
		},
	}

	if err := os.Chdir(dir1); err != nil {
		t.Fatal(err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		_ = h.Complete(ctx1, []string{"run"}, "")
	}()
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		cancel1()
		t.Fatal("first package.json read did not start")
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
		_ = h.Complete(ctx2, []string{"run"}, "")
	}()
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		cancel2()
		t.Fatal("second directory should start a separate package.json read")
	}
	cancel2()
	<-done2

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected separate in-flight package.json reads across directories, got %d", got)
	}
}

func TestNPMHandler_DoesNotCacheCanceledRead(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	var first atomic.Bool
	first.Store(true)
	h := &NPMHandler{
		cacheTTL: time.Minute,
		readFile: func(path string) ([]byte, error) {
			if first.Swap(false) {
				<-release
				close(finished)
			}
			return []byte(`{"scripts": {"build": "tsc"}}`), nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	result := h.Complete(ctx, []string{"run"}, "")
	cancel()
	if len(result.Items) != 0 {
		t.Fatalf("expected no items after timeout, got %#v", result.Items)
	}

	close(release)
	<-finished

	result = h.Complete(context.Background(), []string{"run"}, "")
	if len(result.Items) != 1 || result.Items[0].Value != "build" {
		t.Fatalf("canceled read should not cache an empty result, got %#v", result.Items)
	}
}

type dummyError struct {
	msg string
}

func (e *dummyError) Error() string { return e.msg }
