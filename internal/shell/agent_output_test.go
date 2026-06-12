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

func TestAgentOutputCoordinator_RenderPermission_SanitizesControlChars(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	command := "danger\\n" + "literal\n" + "\x1b[31mowned"
	aoc.RenderPermissionPrompt(command, "Read\nWrite", "#00ff00")

	output := buf.String()
	if strings.Contains(output, "literal\n\x1b[31mowned") {
		t.Fatalf("raw command control characters leaked into prompt: %q", output)
	}
	if strings.Contains(output, "(Read\nWrite)") {
		t.Fatalf("raw tool name newline leaked into prompt: %q", output)
	}
	if !strings.Contains(output, `literal\n\x1b[31mowned`) {
		t.Fatalf("sanitized command missing from prompt: %q", output)
	}
	if !strings.Contains(output, `(Read\nWrite)`) {
		t.Fatalf("sanitized tool name missing from prompt: %q", output)
	}
	if strings.Contains(output, "\x1b[31mowned") {
		t.Fatalf("prompt should not contain the command's raw ANSI sequence: %q", output)
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
		expected []string
	}{
		{"command", ConfirmTypeCommand, []string{"cmd ·", "[Enter: run]"}},
		{"explanation", ConfirmTypeExplanation, []string{"agent ·", "[r: reply]"}},
		{"error", ConfirmTypeError, []string{"error ·", "[Enter: retry]"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			aoc := NewAgentOutputCoordinator(&buf)

			aoc.EnterConfirming()
			aoc.ShowHints(tt.hintType)

			output := buf.String()
			for _, expected := range tt.expected {
				if !strings.Contains(output, expected) {
					t.Errorf("expected %q in output, got: %q", expected, output)
				}
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

func TestAgentOutputCoordinator_StartStreamingDuringPermission(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	// Permission prompt is active before any text has streamed
	// (agent requested a tool around its first text chunk).
	aoc.EnterPermission()

	// First chunk arrives while the prompt is up and the terminal is in raw
	// mode: StartStreaming must not clobber the permission state, and the
	// chunk must be buffered, not written.
	aoc.StartStreaming()
	if aoc.State() != AgentOutputStatePermission {
		t.Fatalf("expected PERMISSION state preserved, got %v", aoc.State())
	}
	aoc.WriteStream("chunk during prompt")
	if strings.Contains(buf.String(), "chunk during prompt") {
		t.Fatalf("text written to terminal during permission prompt: %q", buf.String())
	}

	// Answering the prompt flushes the buffer and resumes streaming.
	aoc.ClearPermissionPrompt(true)
	if !strings.Contains(buf.String(), "chunk during prompt") {
		t.Fatalf("buffered text not flushed after permission cleared: %q", buf.String())
	}
	if aoc.State() != AgentOutputStateStreaming {
		t.Fatalf("expected STREAMING state after clear, got %v", aoc.State())
	}

	// Subsequent chunks must not be dropped.
	aoc.WriteStream(" and after")
	if !strings.Contains(buf.String(), " and after") {
		t.Fatalf("text dropped after permission cleared: %q", buf.String())
	}
}

func TestAgentOutputCoordinator_ClearPermissionPromptAfterCancel(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.RenderPermissionPrompt("rm -rf /tmp/x", "Bash", "")
	aoc.Cancel() // Ctrl+C while the prompt is pending
	buf.Reset()

	// The prompt reader resolves later; it must not clear lines that no
	// longer belong to the prompt.
	aoc.ClearPermissionPrompt(false)
	if got := buf.String(); got != "" {
		t.Fatalf("expected no output after canceled prompt, got %q", got)
	}
}

func TestAgentOutputCoordinator_ClearActiveLine(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	// Normally clears the current line (used to erase the spinner).
	aoc.StartStreaming()
	aoc.ClearActiveLine()
	if got := buf.String(); got != "\r\x1b[K" {
		t.Fatalf("expected clear-line sequence, got %q", got)
	}

	// While a permission prompt owns the screen, clearing would erase the
	// prompt's last line, so it must be skipped.
	buf.Reset()
	aoc.EnterPermission()
	aoc.ClearActiveLine()
	if got := buf.String(); got != "" {
		t.Fatalf("expected no output during permission prompt, got %q", got)
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
