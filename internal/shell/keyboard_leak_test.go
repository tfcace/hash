package shell

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/editor"
)

// A claim on the keyboard that was never released is a bug, but at the
// prompt it must not end the user's login shell: the prompt is the top
// reader, so it takes the keyboard back, says so once, and the shell goes
// on.
func TestPrompt_TakesBackAKeyboardLeftClaimed(t *testing.T) {
	sh := &Shell{}
	sh.editorCfg.Input = editor.NewInputReader(&bytes.Buffer{})
	if _, err := sh.editorCfg.Input.Claim("leaked watcher"); err != nil {
		t.Fatal(err)
	}
	_, claimErr := sh.editorCfg.Input.Claim("editor")
	if claimErr == nil {
		t.Fatal("a second claim succeeded; the test needs the busy error")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	handled := sh.handleInputError(fmt.Errorf("prompt: %w", claimErr))
	os.Stderr = saved
	_ = w.Close()
	var stderr strings.Builder
	_, _ = io.Copy(&stderr, r)
	if handled != nil {
		t.Fatalf("handleInputError = %v, want the shell to go on", handled)
	}
	if owner := sh.editorCfg.Input.Owner(); owner != "" {
		t.Fatalf("keyboard still claimed by %q", owner)
	}
	if !strings.Contains(stderr.String(), "leaked watcher") {
		t.Fatalf("stderr = %q, want it to name the reader that held the keyboard", stderr.String())
	}
}
