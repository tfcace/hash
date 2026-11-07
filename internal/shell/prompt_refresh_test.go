package shell

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/prompt"
)

// starshipStub writes a starship stand-in that logs each call and prints an
// info bar showing the contents of state.
func starshipStub(t *testing.T) (bin, log, state string) {
	t.Helper()
	dir := t.TempDir()
	bin, log, state = filepath.Join(dir, "starship"), filepath.Join(dir, "calls"), filepath.Join(dir, "state")
	script := "#!/bin/sh\necho \"$1\" >> '" + log + "'\nprintf 'INFO:%s\\n> ' \"$(cat '" + state + "')\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	return bin, log, state
}

func starshipCalls(t *testing.T, log string) []string {
	t.Helper()
	time.Sleep(150 * time.Millisecond) // let any background run land
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

// Each prompt runs starship once, in the foreground, and shows what the line
// just did: an empty Enter refreshes the prompt and a command that changes
// prompt state (git switch, jj new) is reflected right away. A background
// precompute ran starship twice more per line and was dropped before it
// could be used.
func TestHandleLine_RunsStarshipOncePerPrompt(t *testing.T) {
	bin, log, state := starshipStub(t)
	sh := newShellForAgentCommand(t)
	sh.prompt = prompt.New(prompt.Config{Mode: "starship", StarshipPath: bin})
	ctx := context.Background()

	for i, line := range []string{"true", "", "echo two > '" + state + "'", ""} {
		out := captureStdout(t, sh, func() { // the line, then the next prompt's head
			_ = sh.handleLine(ctx, line)
			sh.drawPromptHead(nil)
		})
		if calls := starshipCalls(t, log); len(calls) != i+1 {
			t.Fatalf("after line %d (%q) starship ran %d times %v, want %d", i+1, line, len(calls), calls, i+1)
		}
		want := "INFO:one"
		if i >= 2 {
			want = "INFO:two"
		}
		if !strings.Contains(out, want) {
			t.Errorf("after %q the info bar = %q, want %s", line, out, want)
		}
	}
}

// A notice of a background job that ended is printed before the next
// prompt, as bash prints it: above the prompt's info bar, never between
// the bar and the input row. The bar was drawn as the line ended, and the
// notice came after it.
func TestPrompt_JobNoticesComeBeforeTheInfoBar(t *testing.T) {
	sh := newShellForAgentCommand(t)
	bin, _, _ := starshipStub(t)
	sh.prompt = prompt.New(prompt.Config{Mode: "starship", StarshipPath: bin})
	sh.executor.UseInteractiveTerminal() // job notices are the interactive shell's
	captureStdout(t, sh, func() {
		if _, err := sh.executeLine(context.Background(), "sleep 0.1 &", os.Stdout, os.Stderr); err != nil {
			t.Fatal(err)
		}
	})
	time.Sleep(500 * time.Millisecond) // the job ends

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	sh.drawPromptHead(nil)
	os.Stdout, os.Stderr = stdout, stderr
	_ = w.Close()
	data, _ := io.ReadAll(r)
	_ = r.Close()
	out := string(data)
	notice, bar := strings.Index(out, "Done"), strings.Index(out, "INFO:")
	if notice < 0 || bar < 0 || notice > bar {
		t.Errorf("before the prompt: %q; want the job's notice, then the info bar", out)
	}
}
