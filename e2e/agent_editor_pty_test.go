//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/trace"
)

// TestInlineACPHelper is executed as a subprocess by hash: an ACP agent
// that answers every prompt with the text "-la", the fill of an inline
// request (`ls ?? ...`).
func TestInlineACPHelper(t *testing.T) {
	if os.Getenv("HASH_FAKE_ACP") != "inline" {
		return
	}
	if err := trace.Init(); err != nil {
		t.Fatal(err)
	}
	trace.Agent("acp_helper_started", nil)
	runInlineACPHelper(os.Stdin, os.Stdout)
	os.Exit(0)
}

func runInlineACPHelper(in io.Reader, out io.Writer) {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil || request.ID == nil {
			continue
		}
		trace.Agent("acp_helper_request", map[string]any{"method": request.Method})
		switch request.Method {
		case "initialize":
			writeACP(out, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}, "agentInfo": map[string]any{"name": "fake", "version": "1"}}})
		case "session/new":
			writeACP(out, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"sessionId": "fake-session"}})
		case "session/prompt":
			writeACP(out, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fake-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "-la"}}}})
			writeACP(out, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"stopReason": "end_turn"}})
		default:
			writeACP(out, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
		}
	}
}

// Esc with a completion menu open in the agent's inline editor closes the
// menu and keeps the line, as Esc does at the prompt. It declined the
// line: the edit was gone and the prompt came back empty.
func TestAgentEditor_EscClosesTheCompletionMenuAndKeepsTheLine(t *testing.T) {
	testBin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := newTestHome(t)
	h.write(".config/hash/config.toml", testConfig+fmt.Sprintf(`transport = "stdio"
command = %q
args = ["-test.run=^TestInlineACPHelper$", "--"]
timeout = "10s"
allowed_commands_scope = "session"
`, testBin))
	for _, name := range []string{"alpha.txt", "beta.txt"} {
		h.write("~/work/"+name, "")
	}

	ht := startHash(t, h, termSize{}, append(acpTraceEnv(t), "HASH_FAKE_ACP=inline")...)
	ht.waitPrompts(1, startupBound)

	ht.typeText("ls ?? long listing\r")
	ht.waitFor("ls -la", 10*time.Second)
	ht.settle(400 * time.Millisecond)
	ht.typeText("\t") // take the fill into the line
	ht.settle(300 * time.Millisecond)
	ht.typeText(" ")
	ht.settle(200 * time.Millisecond)
	ht.typeText("\t") // the completion menu: the work directory's files
	ht.waitFor("beta.txt", 5*time.Second)
	ht.settle(400 * time.Millisecond)
	prompts := ht.prompts()

	ht.typeText("\x1b")
	ht.settle(600 * time.Millisecond)
	screen := strings.Join(ht.screenLines(), "\n")
	if strings.Contains(screen, "beta.txt") {
		t.Errorf("the completion menu is still open after Esc:\n%s", screen)
	}
	if !strings.Contains(screen, "ls -la") {
		t.Errorf("Esc at the menu dropped the line:\n%s", screen)
	}
	if ht.prompts() != prompts {
		t.Errorf("Esc at the menu ended the edit (a new prompt came):\n%s", screen)
	}

	ht.typeText("\r")
	ht.waitFor("alpha.txt", 5*time.Second)
	ht.waitPrompts(prompts+1, 5*time.Second)
	if got := ht.submittedLines(); len(got) == 0 || !strings.HasPrefix(got[len(got)-1], "ls -la") {
		t.Errorf("Enter after Esc ran %q, want the kept line ls -la ...", got)
	}
	ht.exit()
}
