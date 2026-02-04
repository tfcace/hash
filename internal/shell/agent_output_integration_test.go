package shell

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentOutputCoordinator_FullAgentFlow(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	var wg sync.WaitGroup

	// Simulate streaming loop
	wg.Add(1)
	go func() {
		defer wg.Done()

		aoc.StartStreaming()

		chunks := []string{
			"Here's how to ",
			"find large files:\n",
			"```bash\n",
			"find . -size +100M\n",
			"```\n",
		}

		for _, chunk := range chunks {
			aoc.WriteStream(chunk)
			time.Sleep(10 * time.Millisecond)
		}

		aoc.EndStreaming()
	}()

	// Simulate permission request mid-stream
	wg.Add(1)
	go func() {
		defer wg.Done()

		time.Sleep(25 * time.Millisecond) // Let some chunks through

		aoc.RenderPermissionPrompt("find . -size +100M", "#00ff00")
		time.Sleep(50 * time.Millisecond) // User thinks
		aoc.ClearPermissionPrompt()
	}()

	wg.Wait()

	output := buf.String()

	// Verify key content is present
	mustContain := []string{
		"find large files",
		"Agent wants to run",
		"find . -size +100M",
		"[y]allow",
	}

	for _, expected := range mustContain {
		if !strings.Contains(output, expected) {
			t.Errorf("missing %q in output:\n%s", expected, output)
		}
	}

	// Verify no garbled output (permission text appearing mid-word)
	if strings.Contains(output, "Here's[y]") || strings.Contains(output, "files:[y]") {
		t.Error("permission prompt appears to be interleaved with streaming text")
	}
}

func TestAgentOutputCoordinator_NoHintsOverlap(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.WriteStream("response text\n")
	aoc.EndStreaming()

	// Enter confirming state
	aoc.EnterConfirming()
	aoc.ShowHints(ConfirmTypeCommand)

	// Simulate late permission request (shouldn't happen, but test the guard)
	aoc.RenderPermissionPrompt("dangerous command", "")

	output := buf.String()

	// Count hint occurrences - should only see one set
	runCount := strings.Count(output, "[Enter: run]")
	allowCount := strings.Count(output, "[y]allow")

	if runCount > 1 {
		t.Errorf("hints appear multiple times: run=%d", runCount)
	}
	if allowCount > 1 {
		t.Errorf("permission hints appear multiple times: allow=%d", allowCount)
	}
}
