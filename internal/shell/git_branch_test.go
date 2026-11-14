package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/history"
)

// writeGitFile writes content to path, making the directories it needs.
func writeGitFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Recording a line runs no process: a fork per line (git rev-parse took
// ~15ms on macOS) made hash read a burst of typed lines slower than they
// came, the terminal's input queue backed up past its ring, and macOS
// garbled the queued keys when the next command's cooked mode turned
// line editing back on (`eeeeeeeeee` ran as a command).
func TestRecordCommand_RunsNoGitProcess(t *testing.T) {
	repo := t.TempDir()
	writeGitFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feature/x\n")
	sub := filepath.Join(repo, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	ran := filepath.Join(t.TempDir(), "git-ran")
	writeGitFile(t, filepath.Join(bin, "git"), "#!/bin/sh\n/usr/bin/touch '"+ran+"'\necho fake\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Chdir(sub)

	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	s := &Shell{history: store}
	s.recordCommand("ls", 0, 0)

	if _, err := os.Stat(ran); err == nil {
		t.Error("recording a line ran git")
	}
	recent, err := store.GetRecent(1)
	if err != nil || len(recent) != 1 {
		t.Fatalf("GetRecent = %v, %v", recent, err)
	}
	if recent[0].GitBranch != "feature/x" {
		t.Errorf("GitBranch = %q, want %q", recent[0].GitBranch, "feature/x")
	}
}

func TestGitBranchAt(t *testing.T) {
	t.Run("branch from a subdirectory", func(t *testing.T) {
		repo := t.TempDir()
		writeGitFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
		sub := filepath.Join(repo, "a", "b")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := gitBranchAt(sub); got != "main" {
			t.Errorf("gitBranchAt = %q, want main", got)
		}
	})
	t.Run("detached head is HEAD, as git rev-parse --abbrev-ref says", func(t *testing.T) {
		repo := t.TempDir()
		writeGitFile(t, filepath.Join(repo, ".git", "HEAD"), "67ac7d0123456789abcdef0123456789abcdef01\n")
		if got := gitBranchAt(repo); got != "HEAD" {
			t.Errorf("gitBranchAt = %q, want HEAD", got)
		}
	})
	t.Run("worktree's .git file points at its own HEAD", func(t *testing.T) {
		main := t.TempDir()
		wtDir := filepath.Join(main, ".git", "worktrees", "wt")
		writeGitFile(t, filepath.Join(main, ".git", "HEAD"), "ref: refs/heads/main\n")
		writeGitFile(t, filepath.Join(wtDir, "HEAD"), "ref: refs/heads/topic\n")
		wt := t.TempDir()
		writeGitFile(t, filepath.Join(wt, ".git"), "gitdir: "+wtDir+"\n")
		if got := gitBranchAt(wt); got != "topic" {
			t.Errorf("gitBranchAt = %q, want topic", got)
		}
	})
	t.Run("relative gitdir is relative to the .git file", func(t *testing.T) {
		root := t.TempDir()
		writeGitFile(t, filepath.Join(root, ".git", "modules", "sub", "HEAD"), "ref: refs/heads/dev\n")
		writeGitFile(t, filepath.Join(root, "sub", ".git"), "gitdir: ../.git/modules/sub\n")
		if got := gitBranchAt(filepath.Join(root, "sub")); got != "dev" {
			t.Errorf("gitBranchAt = %q, want dev", got)
		}
	})
	t.Run("reftable repository's placeholder HEAD is no branch", func(t *testing.T) {
		// git init --ref-format=reftable writes HEAD as a stub naming
		// refs/heads/.invalid; the real HEAD is in the reftable.
		repo := t.TempDir()
		writeGitFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/.invalid\n")
		writeGitFile(t, filepath.Join(repo, ".git", "refs", "heads"), "this repository uses the reftable format\n")
		writeGitFile(t, filepath.Join(repo, ".git", "reftable", "tables.list"), "")
		if got := gitBranchAt(repo); got != "" {
			t.Errorf("gitBranchAt = %q, want empty", got)
		}
	})
	t.Run("no repository", func(t *testing.T) {
		if got := gitBranchAt(t.TempDir()); got != "" {
			t.Errorf("gitBranchAt = %q, want empty", got)
		}
	})
}
