//go:build e2e_live

package e2e

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestLiveACPTerminalUX validates the complete terminal path against a real,
// authenticated ACP server. Set HASH_LIVE_ACP_COMMAND (for example
// "agent acp") to opt in; production code contains no vendor selection.
func TestLiveACPTerminalUX(t *testing.T) {
	agentCommand := strings.TrimSpace(os.Getenv("HASH_LIVE_ACP_COMMAND"))
	if agentCommand == "" {
		t.Skip("set HASH_LIVE_ACP_COMMAND, for example: agent acp")
	}
	parts := strings.Fields(agentCommand)
	if len(parts) == 0 {
		t.Fatal("HASH_LIVE_ACP_COMMAND is empty")
	}
	if _, err := exec.LookPath(parts[0]); err != nil {
		t.Fatalf("ACP command %q is not available: %v", parts[0], err)
	}

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
args = %s
timeout = "90s"
allowed_commands_scope = "session"

[prompt]
mode = "built-in"

[shell.startup_files]
login = []
interactive = []
`, parts[0], quotedTOMLStrings(parts[1:]))
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

	transcript := newLivePTYTranscript(terminal)
	transcript.waitFor(t, "❯", 10*time.Second)
	startAt := len(stripLiveTerminalANSI(transcript.String()))
	prompt := "?? In this repository, use two separate read-only inspection tools: first list the root directory, then inspect go.mod. Do not modify files. Briefly report what you observed."
	if _, err := io.WriteString(terminal, prompt+"\r"); err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(75 * time.Second)
	defer deadline.Stop()
	permissionAt := -1
	for {
		select {
		case <-transcript.update:
			plain := stripLiveTerminalANSI(transcript.String())
			if offset := strings.LastIndex(plain, "Agent wants to run"); offset > permissionAt {
				permissionAt = offset
				time.Sleep(50 * time.Millisecond)
				if _, err := io.WriteString(terminal, "y\r"); err != nil {
					t.Fatal(err)
				}
			}
			tail := plain[startAt:]
			running := strings.Index(tail, "agent · running ·")
			finished := strings.Index(tail, "✓")
			if running >= 0 && finished > running && strings.Count(tail, "✓") >= 2 {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for generic ACP lifecycle rows:\n%s", stripLiveTerminalANSI(transcript.String())[startAt:])
		}
	}
}

func quotedTOMLStrings(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

type livePTYTranscript struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	update chan struct{}
}

func newLivePTYTranscript(reader io.Reader) *livePTYTranscript {
	t := &livePTYTranscript{update: make(chan struct{}, 1)}
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

func (t *livePTYTranscript) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

func (t *livePTYTranscript) waitFor(tb testing.TB, needle string, timeout time.Duration) {
	tb.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if strings.Contains(stripLiveTerminalANSI(t.String()), needle) {
			return
		}
		select {
		case <-t.update:
		case <-deadline.C:
			tb.Fatalf("timed out waiting for %q:\n%s", needle, stripLiveTerminalANSI(t.String()))
		}
	}
}

func stripLiveTerminalANSI(text string) string {
	for {
		start := strings.IndexByte(text, 0x1b)
		if start < 0 {
			return text
		}
		end := start + 1
		if end < len(text) && text[end] == '[' {
			end++
			for end < len(text) && (text[end] < '@' || text[end] > '~') {
				end++
			}
			if end < len(text) {
				end++
			}
		} else {
			end++
		}
		text = text[:start] + text[end:]
	}
}
