package shell

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentOutputCoordinator_InitialState(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	if aoc.State() != AgentOutputStateIdle {
		t.Errorf("expected initial state IDLE, got %v", aoc.State())
	}
}

func TestAgentOutputCoordinator_StreamingState(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	if aoc.State() != AgentOutputStateStreaming {
		t.Errorf("expected STREAMING state, got %v", aoc.State())
	}

	aoc.WriteStream("hello ")
	aoc.WriteStream("world")
	aoc.EndStreaming()

	if aoc.State() != AgentOutputStateIdle {
		t.Errorf("expected IDLE state after end, got %v", aoc.State())
	}

	output := buf.String()
	if output != "hello world" {
		t.Errorf("expected 'hello world', got %q", output)
	}
}

func TestAgentOutputCoordinator_PermissionPausesStreaming(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.WriteStream("before ")

	// Permission request arrives
	aoc.EnterPermission()
	if aoc.State() != AgentOutputStatePermission {
		t.Errorf("expected PERMISSION state, got %v", aoc.State())
	}

	// Text arriving during permission should be buffered
	aoc.WriteStream("during ")

	// Permission resolved
	aoc.ExitPermission()
	if aoc.State() != AgentOutputStateStreaming {
		t.Errorf("expected STREAMING state after permission, got %v", aoc.State())
	}

	aoc.WriteStream("after")
	aoc.EndStreaming()

	output := buf.String()
	// Should see: "before " (written) + clear line + "during after" (buffered + resumed)
	// The exact output depends on ANSI codes, but text should be preserved
	if !bytes.Contains([]byte(output), []byte("before")) {
		t.Errorf("missing 'before' in output: %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("during")) {
		t.Errorf("missing 'during' in output: %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("after")) {
		t.Errorf("missing 'after' in output: %q", output)
	}
}

func TestAgentOutputCoordinator_ConcurrentAccess(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()

	// Simulate concurrent permission request during streaming
	var wg sync.WaitGroup

	// Writer goroutine (simulates streaming loop)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			aoc.WriteStream(fmt.Sprintf("chunk%d ", i))
			time.Sleep(time.Microsecond)
		}
	}()

	// Permission goroutine (simulates ACP handler)
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Microsecond) // Let some chunks through
		aoc.EnterPermission()
		time.Sleep(100 * time.Microsecond) // Hold permission
		aoc.ExitPermission()
	}()

	wg.Wait()
	aoc.EndStreaming()

	// Verify no panics occurred and output contains data
	output := buf.String()
	if len(output) == 0 {
		t.Error("expected some output")
	}
}

func TestAgentOutputCoordinator_MultiplePermissionRequests(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.WriteStream("start ")

	// First permission
	aoc.EnterPermission()
	aoc.WriteStream("buffered1 ")
	aoc.ExitPermission()

	aoc.WriteStream("middle ")

	// Second permission
	aoc.EnterPermission()
	aoc.WriteStream("buffered2 ")
	aoc.ExitPermission()

	aoc.WriteStream("end")
	aoc.EndStreaming()

	output := buf.String()

	// All text should appear (order may vary due to buffering)
	for _, expected := range []string{"start", "buffered1", "middle", "buffered2", "end"} {
		if !strings.Contains(output, expected) {
			t.Errorf("missing %q in output: %q", expected, output)
		}
	}
}
