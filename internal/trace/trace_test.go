package trace

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestInit_Disabled(t *testing.T) {
	os.Unsetenv("HASH_TRACE")

	err := Init()
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if global.Load() != nil {
		t.Error("expected global tracer to be nil when HASH_TRACE not set")
	}
}

func TestInit_Enabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-trace.jsonl")

	os.Setenv("HASH_TRACE", "editor")
	os.Setenv("HASH_TRACE_PATH", path)
	defer os.Unsetenv("HASH_TRACE")
	defer os.Unsetenv("HASH_TRACE_PATH")

	err := Init()
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	if global.Load() == nil {
		t.Fatal("expected global tracer to be initialized")
	}

	if !Enabled("editor") {
		t.Error("expected editor subsystem to be enabled")
	}
	if Enabled("agent") {
		t.Error("expected agent subsystem to be disabled")
	}
}

func TestEmit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "emit-test.jsonl")

	os.Setenv("HASH_TRACE", "editor")
	os.Setenv("HASH_TRACE_PATH", path)
	defer os.Unsetenv("HASH_TRACE")
	defer os.Unsetenv("HASH_TRACE_PATH")

	err := Init()
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	Editor("key_read", map[string]any{
		"raw":    []byte{0x63},
		"parsed": "c",
	})

	Close()

	// Read file and verify
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "trace_start") {
		t.Error("missing trace_start entry")
	}
	if !strings.Contains(content, "key_read") {
		t.Error("missing key_read entry")
	}
	if !strings.Contains(content, "trace_end") {
		t.Error("missing trace_end entry")
	}
}

// Emits racing with Close (shutdown while goroutines still trace) must
// neither panic nor race; events after Close are dropped.
func TestEmitDuringClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close-race.jsonl")
	t.Setenv("HASH_TRACE", "all")
	t.Setenv("HASH_TRACE_PATH", path)

	for round := 0; round < 20; round++ {
		if err := Init(); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						Shell("tick", nil)
						_ = Enabled("shell")
					}
				}
			}()
		}
		Close()
		close(stop)
		wg.Wait()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	// Nothing may follow a trace_end in its run: each Init appends a
	// trace_start, so trace_end must be the last line or be followed by one.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i, line := range lines {
		if strings.Contains(line, "trace_end") && i+1 < len(lines) && !strings.Contains(lines[i+1], "trace_start") {
			t.Fatalf("event written after Close: %s", lines[i+1])
		}
	}
}

// A failed write turns the tracer off for good: one message, then silence.
func TestWriteFailureTurnsTheTracerOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path) // read-only: every write fails
	if err != nil {
		t.Fatal(err)
	}
	tr := &Tracer{file: f, subsystems: map[string]bool{"all": true}, level: LevelVerbose}
	tr.writeEntry(Entry{Event: "one"})
	tr.writeEntry(Entry{Event: "two"})
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.file != nil {
		t.Fatal("tracer still on after a failed write")
	}
}
