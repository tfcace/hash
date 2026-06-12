package completion

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestContextLinesReader_StartsFreshReadAfterInflightReadGoesStale(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	reader := &contextLinesReader{}
	readFile := func(path string) ([]string, error) {
		if calls.Add(1) == 1 {
			<-release
			return []string{"stale"}, nil
		}
		return []string{"fresh"}, nil
	}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel1()
	if _, err := reader.read(ctx1, readFile, "config"); err == nil {
		t.Fatal("expected first read to return context timeout")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected first read to start once, got %d calls", got)
	}

	time.Sleep(90 * time.Millisecond)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	lines, err := reader.read(ctx2, readFile, "config")
	if err != nil {
		t.Fatalf("second read error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected second read to start fresh after stale inflight read, got %d calls", got)
	}
	if len(lines) != 1 || lines[0] != "fresh" {
		t.Fatalf("second read lines = %#v, want fresh result", lines)
	}
}

func TestContextBytesReader_StartsFreshReadAfterInflightReadGoesStale(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	reader := &contextBytesReader{}
	readFile := func(path string) ([]byte, error) {
		if calls.Add(1) == 1 {
			<-release
			return []byte("stale"), nil
		}
		return []byte("fresh"), nil
	}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel1()
	if _, err := reader.read(ctx1, readFile, "package.json"); err == nil {
		t.Fatal("expected first read to return context timeout")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected first read to start once, got %d calls", got)
	}

	time.Sleep(90 * time.Millisecond)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	data, err := reader.read(ctx2, readFile, "package.json")
	if err != nil {
		t.Fatalf("second read error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected second read to start fresh after stale inflight read, got %d calls", got)
	}
	if string(data) != "fresh" {
		t.Fatalf("second read data = %q, want fresh result", string(data))
	}
}
