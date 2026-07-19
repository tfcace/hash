package shell

import (
	"os/exec"
	"regexp"
	"strings"
)

// gitPathspecRe matches git's error for a checkout/switch target that does
// not exist, capturing the token the user typed.
var gitPathspecRe = regexp.MustCompile(`pathspec '([^']+)' did not match any file\(s\) known to git`)

// gitDidYouMean suggests a corrected command for a git pathspec failure: when
// the failing token is within edit distance of an existing local branch, it
// returns the original command with the token replaced (e.g. "git checkout
// msater" -> "git checkout master"). Returns "" when no confident correction
// exists. listBranches is only invoked once the error shape and token match,
// so callers pay for branch listing only on actual pathspec typos.
func gitDidYouMean(command, stderr string, listBranches func() []string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 || fields[0] != "git" {
		return ""
	}

	m := gitPathspecRe.FindStringSubmatch(stderr)
	if m == nil {
		return ""
	}
	typo := m[1]

	tokenIdx := -1
	for i, f := range fields[1:] {
		if f == typo {
			tokenIdx = i + 1
			break
		}
	}
	if tokenIdx == -1 {
		return ""
	}

	similar := findSimilar(typo, listBranches(), 1)
	if len(similar) == 0 {
		return ""
	}

	fields[tokenIdx] = similar[0]
	return strings.Join(fields, " ")
}

// gitBranches lists local branch names in the current directory's repository.
func gitBranches() []string {
	out, err := exec.Command("git", "for-each-ref", "--format=%(refname:short)", "refs/heads").Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}
