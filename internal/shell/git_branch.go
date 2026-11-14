package shell

import (
	"os"
	"path/filepath"
	"strings"
)

// gitBranchAt is what `git rev-parse --abbrev-ref HEAD` prints in dir,
// read from the repository's files instead of a git process: the branch
// HEAD names, "HEAD" for a detached head, "" outside a repository and in
// a reftable one (its HEAD file names no branch). History records it for
// every line, and a fork per line (~15ms for git on macOS) made hash read
// a burst of typed lines slower than they came.
func gitBranchAt(dir string) string {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return ""
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: ")
	if !ok {
		return "HEAD"
	}
	if ref == reftableStubRef && isDir(filepath.Join(gitDir, "reftable")) {
		return ""
	}
	return strings.TrimPrefix(ref, "refs/heads/")
}

// reftableStubRef is what a reftable repository's HEAD file names: a
// placeholder for git versions that read HEAD as a file. The branch is in
// the reftable, which hash does not read, so it records none.
const reftableStubRef = "refs/heads/.invalid"

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// findGitDir is the git directory of the repository dir is in: the first
// .git up from dir, a directory, or a file naming one (`gitdir: path`, a
// worktree or a submodule); "" when there is none.
func findGitDir(dir string) string {
	for {
		dotGit := filepath.Join(dir, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			if info.IsDir() {
				return dotGit
			}
			return gitDirFromFile(dotGit)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// gitDirFromFile is the directory a .git file names, relative to the
// file's own directory when the path is relative.
func gitDirFromFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(path), gitDir)
	}
	return gitDir
}
