package completion

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Completion runs on every Tab, often while the user's own git or jj
// command runs, and is killed at its deadline: listing must never write to
// the repository. These tests run the real tools in temporary repositories.

// isolatedVCSEnv keeps the user's git and jj configuration out of a test.
func isolatedVCSEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	config := filepath.Join(t.TempDir(), "jj.toml")
	if err := os.WriteFile(config, []byte("[user]\nname = \"t\"\nemail = \"t@example.com\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JJ_CONFIG", config)
}

func runIn(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitRepoWithChanges makes a repository with a tracked file whose stat
// data changed but not its content (git refreshes such an index entry when
// it may), a modified file, a staged rename, and untracked files.
func gitRepoWithChanges(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolatedVCSEnv(t)
	dir := t.TempDir()
	runIn(t, dir, "git", "init", "-q", ".")
	writeFile(t, filepath.Join(dir, "same.txt"), "same\n")
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), "b\n")
	writeFile(t, filepath.Join(dir, "sub", "c d.txt"), "c\n")
	runIn(t, dir, "git", "add", ".")
	runIn(t, dir, "git", "commit", "-q", "-m", "init")

	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "same.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), "b changed\n")
	runIn(t, dir, "git", "mv", "sub/c d.txt", "sub/e f.txt")
	writeFile(t, filepath.Join(dir, "sub", "new.txt"), "new\n")
	writeFile(t, filepath.Join(dir, "top.txt"), "top\n")
	return dir
}

func TestGitListers_WriteNothing(t *testing.T) {
	dir := gitRepoWithChanges(t)
	index := filepath.Join(dir, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, list := range map[string]vcsLister{
		"refs": defaultListGitRefs, "modified": defaultListGitModified,
		"stashes": defaultListGitStashes, "remotes": defaultListGitRemotes,
	} {
		if _, err := list(ctx, dir); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		after, err := os.ReadFile(index)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("listing %s rewrote .git/index", name)
		}
	}
}

// A repository's config is the repository's, not the user's: a cloned or
// unpacked repository may name a core.fsmonitor program, which git status
// would run on every Tab of git add.
func TestGitListers_RunNoFsmonitor(t *testing.T) {
	dir := gitRepoWithChanges(t)
	marker := filepath.Join(t.TempDir(), "ran")
	runIn(t, dir, "git", "config", "core.fsmonitor", "touch "+marker+"; true #")
	ctx := context.Background()
	for name, list := range map[string]vcsLister{
		"refs": defaultListGitRefs, "modified": defaultListGitModified,
		"stashes": defaultListGitStashes, "remotes": defaultListGitRemotes,
	} {
		if _, err := list(ctx, dir); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("listing %s ran the repository's core.fsmonitor", name)
		}
	}
}

// git add takes paths relative to where it runs: the modified, staged and
// untracked files of the whole repository are listed relative to the
// directory completion runs in, not to the repository root.
func TestDefaultListGitModified_ListsPathsFromTheDirectory(t *testing.T) {
	dir := gitRepoWithChanges(t)
	tests := map[string][]string{
		dir:                       {"sub/b.txt", "sub/e f.txt", "sub/new.txt", "top.txt"},
		filepath.Join(dir, "sub"): {"../top.txt", "b.txt", "e f.txt", "new.txt"},
	}
	for in, want := range tests {
		got, err := defaultListGitModified(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("in %s: modified = %q, want %q", in, got, want)
		}
	}
}

// opCount counts jj's operations from the store itself: asking jj (jj op
// log) would snapshot the working copy and so add one.
func opCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".jj", "repo", "op_store", "operations"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestJJListers_RecordNoOperation(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not installed")
	}
	isolatedVCSEnv(t)
	dir := t.TempDir()
	runIn(t, dir, "jj", "git", "init", "--quiet", ".")
	runIn(t, dir, "jj", "bookmark", "create", "--quiet", "-r", "@", "feature")
	before := opCount(t, dir)
	// A change in the working copy is what a snapshot would record.
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")

	ctx := context.Background()
	revs, err := defaultListJJRevs(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(revs, []string{"@", "@-", "@+", "feature"}) {
		t.Errorf("revs = %q, want @, @-, @+ and feature", revs)
	}
	ids, err := defaultListJJChangeIDs(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Errorf("change ids = %q, want the working copy and the root", ids)
	}
	if after := opCount(t, dir); after != before {
		t.Fatalf("listing recorded %d jj operations", after-before)
	}
}
