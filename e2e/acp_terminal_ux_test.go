//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

var terminalEscapeSequence = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\))`)

// TestDeterministicACPHelper is executed as a subprocess by Hash. It gives the
// terminal test a standards-shaped ACP conversation without a vendor login.
func TestDeterministicACPHelper(t *testing.T) {
	if os.Getenv("HASH_FAKE_ACP") != "1" {
		return
	}
	runDeterministicACPHelper(os.Stdin, os.Stdout)
	os.Exit(0)
}

func TestACPEventTerminalUX(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	hashBin := filepath.Join(tempDir, "hash")
	build := exec.Command("go", "build", "-o", hashBin, "./cmd/hash")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hash: %v\n%s", err, output)
	}

	testBin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configHome := filepath.Join(tempDir, "config")
	configDir := filepath.Join(configHome, "hash")
	dataDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "hash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "hash", "migration.json"), []byte(`{"declined":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`[agent]
transport = "stdio"
command = %q
args = ["-test.run=TestDeterministicACPHelper", "--"]
timeout = "10s"
allowed_commands_scope = "session"

[prompt]
mode = "built-in"

[shell.startup_files]
login = []
interactive = []
`, testBin)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, ".welcome_shown"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(hashBin)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HASH_FAKE_ACP=1",
		"HASH_CONFIG_DIR="+configDir,
		"XDG_CONFIG_HOME="+configHome,
		"XDG_DATA_HOME="+dataDir,
		"TERM=xterm-256color",
	)
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = terminal.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	transcript := newPTYTranscript(terminal)
	transcript.waitFor(t, "hash", 5*time.Second)
	if _, err := io.WriteString(terminal, "?? deterministic ACP UX test\r"); err != nil {
		t.Fatal(err)
	}
	transcript.waitFor(t, "Agent wants to run", 5*time.Second)
	// The permission reader deliberately drains the command submission's
	// trailing newline before it accepts an answer. Give it time to enter raw
	// mode so this approval is not classified as stale input.
	time.Sleep(50 * time.Millisecond)
	if _, err := io.WriteString(terminal, "y\r"); err != nil {
		t.Fatal(err)
	}
	transcript.waitFor(t, "Done.", 5*time.Second)

	plain := stripTerminalANSI(transcript.String())
	if !strings.Contains(plain, "agent · running · pwd") {
		t.Fatalf("missing active tool indicator:\n%s", plain)
	}
	if !strings.Contains(plain, "✓") || !strings.Contains(plain, "execute · pwd") {
		t.Fatalf("missing completed tool row:\n%s", plain)
	}
	if strings.Count(plain, "✓") != 1 {
		t.Fatalf("tool result should have one durable success row:\n%s", plain)
	}
}

type ptyTranscript struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	update chan struct{}
}

func newPTYTranscript(reader io.Reader) *ptyTranscript {
	t := &ptyTranscript{update: make(chan struct{}, 1)}
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := reader.Read(chunk)
			if n > 0 {
				t.mu.Lock()
				t.buf.Write(chunk[:n])
				t.mu.Unlock()
				select {
				case t.update <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return t
}

func (t *ptyTranscript) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

func (t *ptyTranscript) waitFor(tb testing.TB, needle string, timeout time.Duration) {
	tb.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if strings.Contains(stripTerminalANSI(t.String()), needle) {
			return
		}
		select {
		case <-t.update:
		case <-deadline.C:
			tb.Fatalf("timed out waiting for %q:\n%s", needle, stripTerminalANSI(t.String()))
		}
	}
}

func runDeterministicACPHelper(in io.Reader, out io.Writer) {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		switch request.Method {
		case "initialize":
			writeACP(out, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.ID), "result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}, "agentInfo": map[string]any{"name": "fake", "version": "1"}}})
		case "session/new":
			writeACP(out, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.ID), "result": map[string]any{"sessionId": "fake-session"}})
		case "session/prompt":
			writeACP(out, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fake-session", "update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "pwd", "kind": "execute", "status": "pending"}}})
			time.Sleep(120 * time.Millisecond)
			writeACP(out, map[string]any{"jsonrpc": "2.0", "id": 99, "method": "session/request_permission", "params": map[string]any{"sessionId": "fake-session", "toolCall": map[string]any{"toolCallId": "call-1", "title": "pwd", "kind": "execute"}, "options": []map[string]any{{"kind": "allow_once", "optionId": "allow-once"}, {"kind": "reject_once", "optionId": "reject-once"}}}})
			if !scanner.Scan() {
				return
			}
			writeACP(out, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fake-session", "update": map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "in_progress"}}})
			writeACP(out, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fake-session", "update": map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed"}}})
			writeACP(out, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fake-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Do"}}}})
			time.Sleep(60 * time.Millisecond)
			writeACP(out, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fake-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "ne."}}}})
			writeACP(out, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.ID), "result": map[string]any{"stopReason": "end_turn"}})
		}
	}
}

func writeACP(out io.Writer, message any) {
	data, _ := json.Marshal(message)
	_, _ = fmt.Fprintln(out, string(data))
}

func stripTerminalANSI(text string) string {
	return terminalEscapeSequence.ReplaceAllString(text, "")
}
