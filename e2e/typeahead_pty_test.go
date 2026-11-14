//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// numberedLines is n commands recording L0000 ... in numbered.out, each
// ended with Enter. A file keeps the result apart from the terminal driver
// echoing keys typed while hash briefly restores cooked mode between lines.
func numberedLines(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "echo L%04d >> numbered.out\r", i)
	}
	return b.String()
}

// wantNumberedOutput checks that the n numbered lines each ran once, in
// order, and nothing else ran from them.
func wantNumberedOutput(t *testing.T, ht *hashTerm, n int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ht.home.work, "numbered.out"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	ok := len(got) == n
	for i := 0; ok && i < n; i++ {
		ok = got[i] == fmt.Sprintf("L%04d", i)
	}
	if !ok {
		t.Errorf("the %d lines printed %d numbers, want L0000..L%04d in order; first lines %q", n, len(got), n-1, got[:min(len(got), 5)])
	}
	if plain := ht.plain(); strings.Contains(plain, "not found") || strings.Contains(plain, "syntax error") {
		t.Errorf("a garbled line ran:\n%s", plain)
	}
	for _, line := range ht.submittedLines() {
		if !strings.HasPrefix(line, "echo L") && line != "exit" && !strings.HasPrefix(line, "sleep ") {
			t.Errorf("hash ran a line it was not given: %q", line)
		}
	}
}

// A line typed while a command runs and finished after it ended runs whole,
// as in bash: the keys typed during the command come first, then the rest.
// Hash ran the line's tail alone ("2X: command not found").
func TestTypeahead_ALineTypedAcrossACommandsEndRunsWhole(t *testing.T) {
	h := newTestHome(t)
	ht := startHash(t, h, termSize{})
	ht.waitPrompts(1, startupBound)

	ht.typeText("sleep 1\r")
	time.Sleep(300 * time.Millisecond)
	ht.typeText("echo A1\recho B")
	ht.waitUntil("sleep's end", 5*time.Second, func(raw string) bool {
		return strings.Contains(raw, "\x1b]133;D;0")
	})
	time.Sleep(200 * time.Millisecond)
	ht.typeText("2X\r")
	ht.waitFor("B2X", 5*time.Second)
	ht.waitPrompts(4, 5*time.Second)

	if got := ht.outputLines(`A1|B2X|2X`); strings.Join(got, ",") != "A1,B2X" {
		t.Errorf("printed %q, want A1 then B2X", got)
	}
	if plain := ht.plain(); strings.Contains(plain, "not found") {
		t.Errorf("the line's tail ran on its own:\n%s", plain)
	}
	ht.exit()
}

// 300 lines pasted while a command runs all run, once each and in order,
// after it ends.
func TestTypeahead_APasteDuringACommandRunsEveryLine(t *testing.T) {
	h := newTestHome(t)
	ht := startHash(t, h, termSize{})
	ht.waitPrompts(1, startupBound)

	ht.typeText("sleep 1\r")
	time.Sleep(200 * time.Millisecond)
	ht.typeText(numberedLines(300))
	// The PTY can echo L0299 while sleep still runs. Wait for the prompt
	// after every command, not for text the terminal merely echoed.
	ht.waitPrompts(302, 20*time.Second) // initial prompt, sleep, 300 lines

	wantNumberedOutput(t, ht, 300)
	ht.exit()
}

// A fast burst typed at the idle prompt (37-byte chunks every 6 ms, a
// pasting tool's or a fast typist's pace) runs every line unmangled, with
// history on in a git work tree. Hash forked git for each recorded line, so
// it fell behind, the kernel's input queue backed up, and macOS's retype of
// that queue when line mode came back garbled lines ("eeeeeeeeee: command
// not found").
func TestTypeahead_AFastTypedBurstAtTheIdlePromptRunsEveryLine(t *testing.T) {
	h := newTestHome(t)
	h.write("~/work/.git/HEAD", "ref: refs/heads/main\n")
	ht := startHash(t, h, termSize{})
	ht.waitPrompts(1, startupBound)
	time.Sleep(300 * time.Millisecond)

	// About 6 KB/s: a hash that ran a git process for each line (about
	// 15 ms) falls behind, and macOS rewrites the keys queued behind it;
	// hash itself takes well under a millisecond a line, so a machine busy
	// with other tests keeps up (at 4 ms chunks it did not, 3 runs in 10).
	// Faster bursts hit the macOS bug whatever the shell (backlog).
	ht.typeChunks(numberedLines(300), 37, 6*time.Millisecond)
	ht.waitPrompts(301, 20*time.Second)

	wantNumberedOutput(t, ht, 300)
	if got := len(ht.submittedLines()); got != 300 {
		t.Errorf("hash ran %d lines, want 300", got)
	}
	ht.exit()
}
