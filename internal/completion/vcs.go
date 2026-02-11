package completion

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
)

const vcsQueryTimeout = 150 * time.Millisecond

// VCSCompleter provides context-aware completions for common VCS commands.
// It focuses on branch/revision-like arguments where filesystem completion is
// usually not what users want (e.g. "git checkout <TAB>").
type VCSCompleter struct {
	listGitRefs func(context.Context) ([]string, error)
	listJJRevs  func(context.Context) ([]string, error)
}

// NewVCSCompleter creates a new VCS completer.
func NewVCSCompleter() *VCSCompleter {
	return &VCSCompleter{
		listGitRefs: defaultListGitRefs,
		listJJRevs:  defaultListJJRevs,
	}
}

// Name returns the completer name.
func (c *VCSCompleter) Name() string {
	return "vcs"
}

// Complete returns VCS-aware completions for git/jj contexts.
func (c *VCSCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	pipeLine, pipePos := ExtractPipeContext(line, pos)
	segment := pipeLine[:pipePos]
	trailingSpace := strings.HasSuffix(segment, " ")
	parts := strings.Fields(segment)
	if len(parts) < 2 {
		return Result{}, nil
	}

	switch parts[0] {
	case "git":
		return c.completeGit(ctx, parts, trailingSpace), nil
	case "jj":
		return c.completeJJ(ctx, parts, trailingSpace), nil
	default:
		return Result{}, nil
	}
}

func (c *VCSCompleter) completeGit(ctx context.Context, parts []string, trailingSpace bool) Result {
	if len(parts) < 2 {
		return Result{}
	}

	subcommand := parts[1]
	switch subcommand {
	case "checkout", "switch":
		// branch/revision-style completion below
	default:
		return Result{}
	}

	current, before := splitCurrentArg(parts[2:], trailingSpace)
	if !shouldCompleteGitRef(subcommand, current, before) {
		return Result{}
	}

	refs, err := c.listGitRefs(ctx)
	if err != nil {
		return Result{}
	}
	return prefixFilterItems(refs, current)
}

func (c *VCSCompleter) completeJJ(ctx context.Context, parts []string, trailingSpace bool) Result {
	if len(parts) < 2 {
		return Result{}
	}

	subcommand := parts[1]
	switch subcommand {
	case "edit", "new", "show":
		// revision-style completion below
	default:
		return Result{}
	}

	current, before := splitCurrentArg(parts[2:], trailingSpace)
	if !shouldCompleteJJRev(current, before) {
		return Result{}
	}

	revs, err := c.listJJRevs(ctx)
	if err != nil {
		return Result{}
	}
	return prefixFilterItems(revs, current)
}

func splitCurrentArg(args []string, trailingSpace bool) (current string, before []string) {
	if trailingSpace || len(args) == 0 {
		return "", args
	}
	return args[len(args)-1], args[:len(args)-1]
}

func shouldCompleteGitRef(subcommand, current string, before []string) bool {
	if strings.HasPrefix(current, "-") {
		return false
	}
	if containsToken(before, "--") {
		return false
	}

	// If the current argument is immediately after a create-new-branch flag,
	// don't suggest existing refs.
	if len(before) > 0 {
		prev := before[len(before)-1]
		switch subcommand {
		case "checkout":
			if prev == "-b" || prev == "-B" || prev == "--orphan" {
				return false
			}
		case "switch":
			if prev == "-c" || prev == "-C" || prev == "--create" || prev == "--orphan" {
				return false
			}
		}
	}

	// Complete first non-option argument only.
	for _, token := range before {
		if strings.HasPrefix(token, "-") {
			continue
		}
		return false
	}

	return true
}

func shouldCompleteJJRev(current string, before []string) bool {
	if strings.HasPrefix(current, "-") {
		return false
	}
	if containsToken(before, "--") {
		return false
	}

	// Complete only the first positional argument for now.
	for _, token := range before {
		if strings.HasPrefix(token, "-") {
			continue
		}
		return false
	}

	return true
}

func containsToken(tokens []string, target string) bool {
	for _, token := range tokens {
		if token == target {
			return true
		}
	}
	return false
}

func prefixFilterItems(values []string, prefix string) Result {
	items := make([]Item, 0, len(values))
	for _, value := range values {
		if prefix == "" || strings.HasPrefix(value, prefix) {
			items = append(items, Item{
				Value:   value,
				Display: value,
			})
		}
	}
	return Result{Items: items}
}

func defaultListGitRefs(ctx context.Context) ([]string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, vcsQueryTimeout)
	defer cancel()

	lines, err := runIsolatedCommand(
		queryCtx,
		"git",
		"for-each-ref",
		"--format=%(refname:short)",
		"refs/heads",
		"refs/remotes",
		"refs/tags",
	)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(lines))
	refs := make([]string, 0, len(lines))
	for _, line := range lines {
		ref := strings.TrimSpace(line)
		if ref == "" {
			continue
		}
		// Hide synthetic remote HEAD pointers.
		if strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}

	sort.Strings(refs)
	return refs, nil
}

func defaultListJJRevs(ctx context.Context) ([]string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, vcsQueryTimeout)
	defer cancel()

	lines, err := runIsolatedCommand(
		queryCtx,
		"jj",
		"bookmark",
		"list",
		"-T",
		`name ++ "\n"`,
		"--color",
		"never",
	)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{
		"@":  true,
		"@-": true,
		"@+": true,
	}
	revs := []string{"@", "@-", "@+"}
	for _, line := range lines {
		rev := strings.TrimSpace(line)
		if rev == "" {
			continue
		}
		if !seen[rev] {
			seen[rev] = true
			revs = append(revs, rev)
		}
	}

	return revs, nil
}

func runIsolatedCommand(ctx context.Context, command string, args ...string) ([]string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	devNull, err := os.Open(os.DevNull)
	if err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	if err := cmd.Run(); err != nil {
		return nil, err
	}

	raw := strings.TrimRight(stdout.String(), "\n")
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}
