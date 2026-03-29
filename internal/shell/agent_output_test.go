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

func TestAgentOutputCoordinator_RenderPermission(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.WriteStream("response text\n")

	// Render permission prompt
	aoc.RenderPermissionPrompt("kubectl get pods", "", "#00ff00")

	output := buf.String()
	if !strings.Contains(output, "Agent wants to run") {
		t.Errorf("missing permission header in output: %q", output)
	}
	if !strings.Contains(output, "kubectl get pods") {
		t.Errorf("missing command in output: %q", output)
	}
	if !strings.Contains(output, "[y]allow") {
		t.Errorf("missing keybindings in output: %q", output)
	}
}

func TestAgentOutputCoordinator_RenderPermissionWithToolName(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()

	// Render permission prompt with a tool name
	aoc.RenderPermissionPrompt("cat /etc/hosts", "Read", "#00ff00")

	output := buf.String()
	if !strings.Contains(output, "(Read)") {
		t.Errorf("missing tool name context in output: %q", output)
	}
	if !strings.Contains(output, "Agent wants to run") {
		t.Errorf("missing permission header in output: %q", output)
	}
	if !strings.Contains(output, "cat /etc/hosts") {
		t.Errorf("missing command in output: %q", output)
	}
}

func TestAgentOutputCoordinator_RenderPermissionWithoutToolName(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()

	// Render permission prompt without a tool name — should not show parentheses
	aoc.RenderPermissionPrompt("ls -la", "", "#00ff00")

	output := buf.String()
	if strings.Contains(output, "(") {
		t.Errorf("should not contain parentheses when no tool name: %q", output)
	}
}

func TestAgentOutputCoordinator_ClearPermission(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.RenderPermissionPrompt("ls -la", "", "")

	initialLen := buf.Len()

	aoc.ClearPermissionPrompt(true)

	// Should have added cursor-up and clear-line sequences plus feedback
	if buf.Len() <= initialLen {
		t.Error("expected clear sequences to be written")
	}

	output := buf.String()
	// Should contain cursor up sequences (4 lines - we clear current line first)
	if strings.Count(output, "\033[1A") < 4 {
		t.Errorf("expected 4 cursor-up sequences, got fewer in: %q", output)
	}
	// Should show allowed feedback with the command
	if !strings.Contains(output, "✓") || !strings.Contains(output, "ls -la") {
		t.Errorf("expected allowed feedback with command, got: %q", output)
	}
}

func TestAgentOutputCoordinator_ShowHints(t *testing.T) {
	tests := []struct {
		name     string
		hintType ConfirmationType
		expected string
	}{
		{"command", ConfirmTypeCommand, "[Enter: run]"},
		{"explanation", ConfirmTypeExplanation, "[Enter: ok]"},
		{"error", ConfirmTypeError, "[Enter: retry]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			aoc := NewAgentOutputCoordinator(&buf)

			aoc.EnterConfirming()
			aoc.ShowHints(tt.hintType)

			output := buf.String()
			if !strings.Contains(output, tt.expected) {
				t.Errorf("expected %q in output, got: %q", tt.expected, output)
			}
		})
	}
}

func TestAgentOutputCoordinator_HintsOnlyInConfirmingState(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	// Should not show hints when not in confirming state
	aoc.ShowHints(ConfirmTypeCommand)

	if strings.Contains(buf.String(), "[Enter:") {
		t.Error("hints should not appear when not in confirming state")
	}
}

func TestAgentOutputCoordinator_CancelDuringPermission(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.WriteStream("partial response")

	aoc.EnterPermission()
	aoc.WriteStream("buffered text") // Would be buffered

	// Simulate cancel - should clean up and not crash
	aoc.Cancel()

	if aoc.State() != AgentOutputStateIdle {
		t.Errorf("expected IDLE after cancel, got %v", aoc.State())
	}
}

func TestAgentOutputCoordinator_CancelDuringPermission_Clears5Lines(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.RenderPermissionPrompt("rm -rf /tmp", "", "")

	// Record where we are before cancel
	beforeCancel := buf.Len()
	aoc.Cancel()

	cancelOutput := buf.String()[beforeCancel:]

	// The permission prompt has 5 lines (blank + header + command + spacer + keybindings).
	// Cancel should: clear current line (1), then cursor-up+clear 4 more times = 5 lines total.
	cursorUpCount := strings.Count(cancelOutput, "\033[1A")
	if cursorUpCount != 4 {
		t.Errorf("expected exactly 4 cursor-up sequences (clear 5 lines total), got %d in: %q",
			cursorUpCount, cancelOutput)
	}

	// Should clear each line
	clearLineCount := strings.Count(cancelOutput, "\033[2K")
	if clearLineCount != 5 {
		t.Errorf("expected 5 clear-line sequences, got %d in: %q", clearLineCount, cancelOutput)
	}
}

func TestAgentOutputCoordinator_CancelDuringStreaming_ClearsOneLine(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.WriteStream("partial")

	beforeCancel := buf.Len()
	aoc.Cancel()

	cancelOutput := buf.String()[beforeCancel:]

	// When not in permission state, Cancel should just clear current line
	if !strings.Contains(cancelOutput, "\x1b[K") {
		t.Errorf("expected clear-to-end-of-line in cancel output, got: %q", cancelOutput)
	}
	// Should NOT have cursor-up sequences
	if strings.Contains(cancelOutput, "\033[1A") {
		t.Errorf("should not have cursor-up sequences when canceling outside permission, got: %q", cancelOutput)
	}
}

func TestAgentOutputCoordinator_ClearPermissionPrompt_MatchesCancel_LineCount(t *testing.T) {
	// Both ClearPermissionPrompt and Cancel should clear exactly 5 lines
	// when in permission state. This is a regression test for the off-by-one fix.
	var clearBuf, cancelBuf bytes.Buffer

	// Test ClearPermissionPrompt
	aoc1 := NewAgentOutputCoordinator(&clearBuf)
	aoc1.StartStreaming()
	aoc1.RenderPermissionPrompt("test", "", "")
	beforeClear := clearBuf.Len()
	aoc1.ClearPermissionPrompt(true)
	clearOutput := clearBuf.String()[beforeClear:]
	clearUpCount := strings.Count(clearOutput, "\033[1A")

	// Test Cancel
	aoc2 := NewAgentOutputCoordinator(&cancelBuf)
	aoc2.StartStreaming()
	aoc2.RenderPermissionPrompt("test", "", "")
	beforeCancel := cancelBuf.Len()
	aoc2.Cancel()
	cancelOutput := cancelBuf.String()[beforeCancel:]
	cancelUpCount := strings.Count(cancelOutput, "\033[1A")

	// Both should use 4 cursor-ups (clear current line first, then up 4 more)
	if clearUpCount != cancelUpCount {
		t.Errorf("ClearPermissionPrompt and Cancel should clear same number of lines: clear=%d, cancel=%d",
			clearUpCount, cancelUpCount)
	}
	if clearUpCount != 4 {
		t.Errorf("expected 4 cursor-ups, got %d", clearUpCount)
	}
}

func TestAgentOutputCoordinator_PermissionWithoutStreaming(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	// Permission request when not streaming (edge case)
	aoc.RenderPermissionPrompt("whoami", "", "")

	output := buf.String()
	if !strings.Contains(output, "whoami") {
		t.Errorf("permission prompt should still render: %q", output)
	}

	aoc.ClearPermissionPrompt(true)

	if aoc.State() != AgentOutputStateIdle {
		t.Errorf("should return to IDLE, got %v", aoc.State())
	}
}

func TestAgentOutputCoordinator_ClearPermission_Denied(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.RenderPermissionPrompt("rm -rf /", "", "")

	initialLen := buf.Len()

	aoc.ClearPermissionPrompt(false)

	// Should have added cursor-up and clear-line sequences plus feedback
	if buf.Len() <= initialLen {
		t.Error("expected clear sequences to be written")
	}

	output := buf.String()
	// Should show denied feedback with the command
	if !strings.Contains(output, "✗") || !strings.Contains(output, "rm -rf /") {
		t.Errorf("expected denied feedback with command, got: %q", output)
	}
}
