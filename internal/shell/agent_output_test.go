package shell

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
)

func TestAgentOutputCoordinator_ToolLifecycleFinalizesOnce(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.BeginRequest()

	if update, final := aoc.RecordToolUpdate(agent.ToolCallUpdate{ID: "1", Title: "pwd", Kind: "execute", Status: agent.ToolCallPending}); final || update.Title != "pwd" {
		t.Fatalf("pending result = %#v, final=%v", update, final)
	}
	update, final := aoc.RecordToolUpdate(agent.ToolCallUpdate{ID: "1", Status: agent.ToolCallCompleted})
	if !final || update.Title != "pwd" || update.Kind != "execute" {
		t.Fatalf("completed result = %#v, final=%v", update, final)
	}
	if _, final := aoc.RecordToolUpdate(agent.ToolCallUpdate{ID: "1", Status: agent.ToolCallCompleted}); final {
		t.Fatal("duplicate completion should not finalize twice")
	}

	aoc.RenderToolResult(update, "")
	if got := buf.String(); !strings.Contains(got, "execute") || !strings.Contains(got, "pwd") || !strings.Contains(got, "✓") {
		t.Fatalf("tool result = %q", got)
	}
}

func TestAgentOutputCoordinator_DeniedToolRendersSingleSafeRow(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.BeginRequest()
	aoc.RecordToolUpdate(agent.ToolCallUpdate{ID: "1", Title: "bad\n\x1b[31m", Kind: "execute", Status: agent.ToolCallPending})
	update, final := aoc.ResolveToolPermission("1", false)
	if !final {
		t.Fatal("denied tool should finalize immediately")
	}
	aoc.RenderToolResult(update, "")
	got := buf.String()
	if !strings.Contains(got, "denied") || strings.Contains(got, "\x1b[31m") || !strings.Contains(got, `bad\n\x1b[31m`) {
		t.Fatalf("unsafe denied row = %q", got)
	}
}

// promptUp reports whether a permission prompt is on screen.
func promptUp(aoc *AgentOutputCoordinator) bool {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	return aoc.prompt != nil
}

func TestAgentOutputCoordinator_InitialState(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	if promptUp(aoc) {
		t.Errorf("want no prompt up")
	}
}

func TestAgentOutputCoordinator_StreamingState(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	if promptUp(aoc) {
		t.Errorf("want no prompt up")
	}

	aoc.WriteStream("hello ")
	aoc.WriteStream("world")

	if promptUp(aoc) {
		t.Errorf("want no prompt up")
	}

	output := buf.String()
	if output != "hello world" {
		t.Errorf("expected 'hello world', got %q", output)
	}
}

func TestAgentOutputCoordinator_PermissionPausesStreaming(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.WriteStream("before ")

	// Permission request arrives
	aoc.RenderPermissionPrompt("ls", "", "")
	if !promptUp(aoc) {
		t.Errorf("want the permission prompt up")
	}

	// Text arriving during permission should be buffered
	aoc.WriteStream("during ")

	// Permission resolved
	aoc.ClearPermissionPrompt()
	if promptUp(aoc) {
		t.Errorf("want no prompt up")
	}

	aoc.WriteStream("after")

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
		aoc.RenderPermissionPrompt("ls", "", "")
		time.Sleep(100 * time.Microsecond) // Hold permission
		aoc.ClearPermissionPrompt()
	}()

	wg.Wait()

	// Verify no panics occurred and output contains data
	output := buf.String()
	if len(output) == 0 {
		t.Error("expected some output")
	}
}

func TestAgentOutputCoordinator_MultiplePermissionRequests(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.WriteStream("start ")

	// First permission
	aoc.RenderPermissionPrompt("ls", "", "")
	aoc.WriteStream("buffered1 ")
	aoc.ClearPermissionPrompt()

	aoc.WriteStream("middle ")

	// Second permission
	aoc.RenderPermissionPrompt("ls", "", "")
	aoc.WriteStream("buffered2 ")
	aoc.ClearPermissionPrompt()

	aoc.WriteStream("end")

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
	if !strings.Contains(output, "y to allow") {
		t.Errorf("missing keybindings in output: %q", output)
	}
}

func TestAgentOutputCoordinator_RenderPermissionWithToolName(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

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

// Clearing an answered prompt erases its five rows and adds none: the
// tool's lifecycle row says whether it ran.
func TestAgentOutputCoordinator_ClearPermission(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.RenderPermissionPrompt("ls -la", "", "")
	buf.Reset()
	aoc.ClearPermissionPrompt()

	if got := buf.String(); got != clearPermissionRows {
		t.Errorf("clear wrote %q, want just the prompt's rows erased", got)
	}
	if promptUp(aoc) {
		t.Error("prompt still up after clear")
	}
}

func TestAgentOutputCoordinator_ShowHints(t *testing.T) {
	tests := []struct {
		name     string
		hintType ConfirmationType
		expected []string
	}{
		{"command", ConfirmTypeCommand, []string{"cmd ·", "enter to run"}},
		{"explanation", ConfirmTypeExplanation, []string{"agent ·", "r to reply"}},
		{"error", ConfirmTypeError, []string{"error ·", "enter to retry"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			aoc := NewAgentOutputCoordinator(&buf)

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

func TestAgentOutputCoordinator_DismissDuringPermission(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.WriteStream("partial response")

	aoc.RenderPermissionPrompt("ls", "", "")
	aoc.WriteStream("buffered text") // Would be buffered

	// The request ended under the prompt: its rows go, and what it held
	// goes with the rest of the reply.
	aoc.DismissPermissionPrompt()

	if promptUp(aoc) {
		t.Error("prompt still up after dismiss")
	}
	if strings.Contains(buf.String(), "buffered text") {
		t.Errorf("held text drawn after dismiss: %q", buf.String())
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

	aoc.ClearPermissionPrompt()

	if promptUp(aoc) {
		t.Errorf("want no prompt up")
	}
}

func TestAgentOutputCoordinator_FirstChunkDuringPermission(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	// Permission prompt is active before any text has streamed
	// (agent requested a tool around its first text chunk).
	aoc.RenderPermissionPrompt("ls", "", "")

	// First chunk arrives while the prompt is up: it must be held, not
	// written.
	aoc.WriteStream("chunk during prompt")
	if strings.Contains(buf.String(), "chunk during prompt") {
		t.Fatalf("text written to terminal during permission prompt: %q", buf.String())
	}

	// Answering the prompt flushes the buffer and resumes streaming.
	aoc.ClearPermissionPrompt()
	if !strings.Contains(buf.String(), "chunk during prompt") {
		t.Fatalf("buffered text not flushed after permission cleared: %q", buf.String())
	}
	if promptUp(aoc) {
		t.Fatalf("want no prompt up")
	}

	// Subsequent chunks must not be dropped.
	aoc.WriteStream(" and after")
	if !strings.Contains(buf.String(), " and after") {
		t.Fatalf("text dropped after permission cleared: %q", buf.String())
	}
}

func TestAgentOutputCoordinator_ClearPermissionPromptAfterDismiss(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.RenderPermissionPrompt("rm -rf /tmp/x", "Bash", "")
	aoc.DismissPermissionPrompt() // Ctrl+C while the prompt is pending
	buf.Reset()

	// The prompt reader resolves later; it must not clear lines that no
	// longer belong to the prompt.
	aoc.ClearPermissionPrompt()
	if got := buf.String(); got != "" {
		t.Fatalf("expected no output after canceled prompt, got %q", got)
	}
}

// A prompt drawn on the spinner's row (cleared) gives that row back when
// it clears: the cursor ends where the prompt began, so the tool row that
// follows takes the spinner's place.
func TestAgentOutputCoordinator_PermissionPromptOnFreeRowReturnsToIt(t *testing.T) {
	var buf lockedBuffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.ShowState(AgentStateThinking)
	aoc.RenderPermissionPrompt("ls -la", "", "")
	aoc.ClearPermissionPrompt()

	out := buf.String()
	if down, up := strings.Count(out, "\n"), strings.Count(out, ansiCursorUp); down != up {
		t.Errorf("moved down %d rows and up %d: cursor did not return to the prompt's row", down, up)
	}
}

// A failed tool keeps its kind, like every other row, and says why it
// failed: "✗ failed · Read x" told the user neither.
func TestAgentOutputCoordinator_FailedToolShowsKindAndReason(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.RenderToolResult(agent.ToolCallUpdate{
		Kind:    "read",
		Title:   "Read does-not-exist.txt",
		Status:  agent.ToolCallFailed,
		Content: "<tool_use_error>File does not exist.\nCurrent directory: /tmp</tool_use_error>",
	}, "")

	got := stripANSI(buf.String())
	want := "  ✗ read · Read does-not-exist.txt · File does not exist.\n"
	if got != want {
		t.Errorf("failed row = %q, want %q", got, want)
	}
}

// A canceled turn ends with a row in the tool-row style, not a bare
// "hash: request canceled".
func TestShowTurnCanceled(t *testing.T) {
	var buf bytes.Buffer
	showTurnCanceled(&buf, "")
	if got, want := stripANSI(buf.String()), "  – canceled\n"; got != want {
		t.Errorf("canceled row = %q, want %q", got, want)
	}
}

var testSGRRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return testSGRRe.ReplaceAllString(s, "") }

// Text held during a permission prompt and drawn after it counts toward the
// row state, so the footer neither doubles a newline nor joins the row.
func TestAgentOutputCoordinator_BufferedTextSetsRowState(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.WriteStream("line one\n")
	aoc.RenderPermissionPrompt("ls", "", "")
	aoc.WriteStream("half a row")
	aoc.ClearPermissionPrompt()
	if rowOf(aoc) != rowMidReply {
		t.Error("the row is not mid-reply after buffered text without a newline")
	}
}

// A turn canceled before any text read the previous turn's unfinished row
// and ended a row that was not there: a stray blank row before "canceled".
func TestAgentOutputCoordinator_NewTurnForgetsThePreviousTurnsRow(t *testing.T) {
	aoc := NewAgentOutputCoordinator(&bytes.Buffer{})
	aoc.BeginTurn()
	aoc.WriteStream("a reply that ends mid-row")

	aoc.BeginTurn() // the next turn, canceled before its first chunk
	if rowOf(aoc) == rowMidReply {
		t.Error("a new turn reports the previous turn's reply as unfinished")
	}
}

// A turn's retry is a new ACP prompt, whose tool IDs may repeat the failed
// one's: a tool finished there must not keep the retry's from finishing.
func TestAgentOutputCoordinator_EachRequestHasItsOwnTools(t *testing.T) {
	aoc := NewAgentOutputCoordinator(&bytes.Buffer{})
	aoc.BeginTurn()
	for i := range 2 {
		aoc.BeginRequest()
		if _, final := aoc.RecordToolUpdate(agent.ToolCallUpdate{ID: "1", Status: agent.ToolCallCompleted}); !final {
			t.Errorf("request %d: tool 1 did not finish", i+1)
		}
	}
}

// A finished tool's row that lands while a permission prompt owns the
// screen waits behind the prompt, then shows once it clears, even when no
// reply had streamed yet.
func TestAgentOutputCoordinator_ToolRowDuringAPromptWaitsForIt(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.RenderPermissionPrompt("rm -rf build", "Bash", "")
	aoc.RenderToolResult(agent.ToolCallUpdate{Title: "ls", Kind: "execute", Status: agent.ToolCallCompleted}, "")
	if strings.Contains(buf.String(), "execute") {
		t.Fatalf("tool row drawn over the prompt: %q", buf.String())
	}
	aoc.ClearPermissionPrompt()
	if !strings.Contains(buf.String(), "execute · ls") {
		t.Errorf("tool row lost once the prompt cleared: %q", buf.String())
	}
	if promptUp(aoc) {
		t.Errorf("want no prompt up")
	}
}

// rowOf reports what the coordinator says the cursor's row holds.
func rowOf(aoc *AgentOutputCoordinator) replyRow {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	return aoc.row
}
