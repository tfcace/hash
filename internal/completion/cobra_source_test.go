package completion

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeCobra writes an executable that answers `__complete` with the
// given output after sleeping, appending one line to a counter file per run.
func writeFakeCobra(t *testing.T, name, sleep, output string) (cmdPath string, runs func() int) {
	t.Helper()
	dir := t.TempDir()
	cmdPath = filepath.Join(dir, name)
	counter := filepath.Join(dir, "runs")
	script := "#!/bin/sh\necho run >> '" + counter + "'\n"
	if sleep != "" {
		script += "sleep " + sleep + "\n"
	}
	script += "printf '" + output + "'\n"
	if err := os.WriteFile(cmdPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return cmdPath, func() int {
		data, _ := os.ReadFile(counter)
		return strings.Count(string(data), "run")
	}
}

// A fetch slower than the failure backoff (which used to double as the
// in-flight window, here shrunk) must not be relaunched while it is still
// running: in-flight and backing off are separate states.
func TestCobraCompleter_SlowFetchIsNotDuplicated(t *testing.T) {
	cmdPath, runs := writeFakeCobra(t, "slowcobra", "0.4", `pod-a\n:4\n`)
	c := NewCobraCompleter("slowcobra")
	c.resolvePath = func(string) (string, error) { return cmdPath, nil }
	c.policy.failureTTL = 100 * time.Millisecond

	line := "slowcobra get "
	c.Prefetch(line, len(line))
	time.Sleep(200 * time.Millisecond)
	c.Prefetch(line, len(line))
	time.Sleep(400 * time.Millisecond)
	if got := runs(); got != 1 {
		t.Fatalf("__complete ran %d times, want 1", got)
	}
}

// A recalled or pasted line gets no space-triggered prefetch; Tab itself
// must start the fetch so a later Tab can answer.
func TestCobraCompleter_TabWithoutPrefetchStartsFetch(t *testing.T) {
	cmdPath, _ := writeFakeCobra(t, "kubectl", "", `pod-a\n:4\n`)
	c := NewCobraCompleter()
	c.resolvePath = func(string) (string, error) { return cmdPath, nil }

	if _, ok := tabUntil(t, c, "kubectl get pods ", "pod-a", 2*time.Second); !ok {
		t.Fatal("Tab on a recalled line never produced cobra completions")
	}
}

// ShellCompDirectiveNoFileComp (4) with no candidates means "nothing
// matches, and no files either": the argument is owned, not fallen through.
func TestCobraCompleter_NoFileCompDirectiveOwnsEmptyAnswer(t *testing.T) {
	cmdPath, _ := writeFakeCobra(t, "kubectl", "", `:4\n`)
	c := NewCobraCompleter()
	c.resolvePath = func(string) (string, error) { return cmdPath, nil }

	line := "kubectl get pods "
	c.Prefetch(line, len(line))
	var result Result
	if !eventually(2*time.Second, func() bool {
		result, _ = c.Complete(context.Background(), line, len(line))
		return result.Outcome() != OutcomePending
	}) {
		t.Fatal("fetch never landed")
	}
	if got := result.Outcome(); got != OutcomeNoMatches {
		t.Fatalf("outcome = %v, want no_matches for an empty NoFileComp answer", got)
	}
}

// Tools like gh answer per repository: a result cached in one directory
// must not answer after a cd, and __complete must run in the directory the
// user is in, not wherever the shell process started.
func TestCobraCompleter_ResultsFollowTheWorkingDirectory(t *testing.T) {
	cmdPath, _ := writeFakeCobra(t, "fakegh", "", `'"$(basename "$PWD")"'\n:4\n`)
	repoA := filepath.Join(t.TempDir(), "repoA")
	repoB := filepath.Join(t.TempDir(), "repoB")
	for _, dir := range []string{repoA, repoB} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cwd := repoA
	c := NewCobraCompleter("fakegh")
	c.resolvePath = func(string) (string, error) { return cmdPath, nil }
	c.getwd = func() string { return cwd }

	line := "fakegh pr checkout "
	if _, ok := tabUntil(t, c, line, "repoA", 2*time.Second); !ok {
		t.Fatal("in repoA: __complete did not run in repoA")
	}
	cwd = repoB
	if result, ok := tabUntil(t, c, line, "repoB", 2*time.Second); !ok {
		t.Fatalf("after cd repoB: items %#v, want repoB's answer", result.Items)
	}
}

// Activating a venv or entering a direnv directory changes PATH, and with it
// which binary a tool name runs: the old binary's resolved path and its
// answers must not stand in for the new one's.
func TestCobraCompleter_ResolvesAgainWhenPATHChanges(t *testing.T) {
	pathA, _ := writeFakeCobra(t, "fakevenv", "", `fromA\n:4\n`)
	pathB, _ := writeFakeCobra(t, "fakevenv", "", `fromB\n:4\n`)
	c := NewCobraCompleter("fakevenv")
	c.resolvePath = exec.LookPath

	line := "fakevenv run "
	t.Setenv("PATH", filepath.Dir(pathA))
	if _, ok := tabUntil(t, c, line, "fromA", 2*time.Second); !ok {
		t.Fatal("with PATH at A: no answer from A's binary")
	}
	t.Setenv("PATH", filepath.Dir(pathB))
	if result, ok := tabUntil(t, c, line, "fromB", 2*time.Second); !ok {
		t.Fatalf("after PATH moved to B: items %#v, want B's answer", result.Items)
	}
}
