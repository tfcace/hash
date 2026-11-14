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

	}()

	// Simulate permission request mid-stream
	wg.Add(1)
	go func() {
		defer wg.Done()

		time.Sleep(25 * time.Millisecond) // Let some chunks through

		aoc.RenderPermissionPrompt("find . -size +100M", "", "#00ff00")
		time.Sleep(50 * time.Millisecond) // User thinks
		aoc.ClearPermissionPrompt()       // User allowed
	}()

	wg.Wait()

	output := buf.String()

	// Verify key content is present
	mustContain := []string{
		"find large files",
		"Agent wants to run",
		"find . -size +100M",
		"y to allow",
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
