package completion

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tfcace/hash/internal/trace"
)

// vcsQueryTimeout bounds one git/jj query. Queries run in the background,
// detached from the Tab that asked, so a large repository whose
// for-each-ref outlasts the editor's completion budget still fills the cache
// and the menu fills itself in through the ready notification.
const vcsQueryTimeout = 2 * time.Second

// vcsLister lists candidates by running a VCS command in dir. The directory
// is explicit because queries run detached: by the time one starts, the
// shell may have cd'd elsewhere.
type vcsLister func(ctx context.Context, dir string) ([]string, error)

// VCSCompleter provides context-aware completions for common VCS commands.
// It focuses on branch/revision-like arguments where filesystem completion is
// usually not what users want (e.g. "git checkout <TAB>").
type VCSCompleter struct {
	listGitRefs     vcsLister
	listJJRevs      vcsLister
	listGitModified vcsLister
	listGitStashes  vcsLister
	listGitRemotes  vcsLister
	listJJChangeIDs vcsLister

	policy  flightPolicy
	lookups flightCache[[]string] // keyed by kind and directory
	ready   readyNotifier
	getwd   func() string
}

// NewVCSCompleter creates a new VCS completer.
func NewVCSCompleter() *VCSCompleter {
	c := &VCSCompleter{
		listGitRefs:     defaultListGitRefs,
		listJJRevs:      defaultListJJRevs,
		listGitModified: defaultListGitModified,
		listGitStashes:  defaultListGitStashes,
		listGitRemotes:  defaultListGitRemotes,
		listJJChangeIDs: defaultListJJChangeIDs,
		policy: flightPolicy{
			ttl: 2 * time.Second,
			// Outside a repository every query fails; don't rerun it on
			// every Tab.
			failureTTL: 2 * time.Second,
			timeout:    vcsQueryTimeout,
			wait:       sourceWait,
		},
		getwd: defaultGetwd,
	}
	c.lookups.ready = &c.ready
	return c
}

// SetOnReady registers a callback fired whenever a background query lands,
// so a "fetching" menu can fill itself in without another Tab.
func (c *VCSCompleter) SetOnReady(fn func()) {
	c.ready.set(fn)
}

// Name returns the completer name.
func (c *VCSCompleter) Name() string {
	return "vcs"
}

// Complete returns VCS-aware completions for git/jj contexts.
func (c *VCSCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	cw := parseCommandWords(line, pos)
	// Needs a complete subcommand: "git chec<TAB>" is not ours.
	args := cw.args()
	if len(args) == 0 || !cw.inArgumentPosition() {
		return Result{}, nil
	}

	switch cw.command() {
	case "git":
		return c.completeGit(ctx, args[0], args[1:], cw.current), nil
	case "jj":
		return c.completeJJ(ctx, args[0], args[1:], cw.current), nil
	default:
		return Result{}, nil
	}
}

func (c *VCSCompleter) completeGit(ctx context.Context, subcommand string, before []string, current string) Result {

	switch subcommand {
	case "checkout", "switch", "log", "diff", "rebase", "cherry-pick", "revert", "merge":
		if !shouldCompleteGitRef(subcommand, current, before) {
			return Result{}
		}
		return c.lookupAndFilter(ctx, "git_refs", c.listGitRefs, current)

	case "add":
		if strings.HasPrefix(current, "-") {
			return Result{}
		}
		return c.lookupAndFilter(ctx, "git_modified", c.listGitModified, current)

	case "branch":
		return c.completeGitBranch(ctx, current, before)

	case "stash":
		return c.completeGitStash(ctx, current, before)

	case "remote":
		return c.completeGitRemote(ctx, current, before)

	default:
		return Result{}
	}
}

func (c *VCSCompleter) completeGitBranch(ctx context.Context, current string, before []string) Result {
	if containsToken(before, "-d") || containsToken(before, "-D") {
		return c.lookupAndFilter(ctx, "git_refs", c.listGitRefs, current)
	}
	return Result{}
}

func (c *VCSCompleter) completeGitStash(ctx context.Context, current string, before []string) Result {
	if len(before) == 0 {
		return Result{}
	}
	switch before[0] {
	case "pop", "apply", "drop", "show":
		return c.lookupAndFilter(ctx, "git_stashes", c.listGitStashes, current)
	default:
		return Result{}
	}
}

func (c *VCSCompleter) completeGitRemote(ctx context.Context, current string, before []string) Result {
	if len(before) == 0 {
		return Result{}
	}
	switch before[0] {
	case "remove", "rename", "show", "prune":
		return c.lookupAndFilter(ctx, "git_remotes", c.listGitRemotes, current)
	default:
		return Result{}
	}
}

// lookupAndFilter runs a list function (through the cache) and
// prefix-filters the results.
func (c *VCSCompleter) lookupAndFilter(ctx context.Context, kind string, listFn vcsLister, prefix string) Result {
	start := time.Now()
	traceEnabled := trace.Enabled("completion")
	if traceEnabled {
		trace.Emit("completion", "vcs_lookup_start", trace.LevelDetailed, map[string]any{
			"kind":   kind,
			"prefix": prefix,
		})
	}

	values, status := c.lookupValues(ctx, kind, listFn)
	result := sourceResult(values, status, prefix)
	if traceEnabled {
		trace.Emit("completion", "vcs_lookup_done", trace.LevelDetailed, map[string]any{
			"kind":        kind,
			"prefix":      prefix,
			"values":      len(values),
			"items":       len(result.Items),
			"status":      status.String(),
			"duration_ms": float64(time.Since(start).Microseconds()) / 1000.0,
		})
	}
	return result
}

// lookupValues returns the candidates of one kind for the working directory.
// The returned slice is shared with the cache and must not be modified.
func (c *VCSCompleter) lookupValues(ctx context.Context, kind string, listFn vcsLister) ([]string, lookupStatus) {
	dir := c.getwd()
	return c.lookups.get(ctx, kind+"\x00"+dir, c.policy, func(ctx context.Context) ([]string, error) {
		return listFn(ctx, dir)
	})
}

func (c *VCSCompleter) completeJJ(ctx context.Context, subcommand string, before []string, current string) Result {

	switch subcommand {
	case "edit", "new", "show":
		if !shouldCompleteJJRev(current, before) {
			return Result{}
		}
		return c.completeJJRevision(ctx, current)

	case "bookmark":
		return c.completeJJBookmark(ctx, current, before)

	case "describe", "abandon", "squash":
		if !shouldCompleteJJRev(current, before) {
			return Result{}
		}
		return c.lookupAndFilter(ctx, "jj_change_ids", c.listJJChangeIDs, current)

	default:
		return Result{}
	}
}

func (c *VCSCompleter) completeJJRevision(ctx context.Context, current string) Result {
	// Run both jj queries in parallel
	var revs, ids []string
	var revsStatus, idsStatus lookupStatus
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		revs, revsStatus = c.lookupValues(ctx, "jj_revs", c.listJJRevs)
	}()
	go func() {
		defer wg.Done()
		ids, idsStatus = c.lookupValues(ctx, "jj_change_ids", c.listJJChangeIDs)
	}()
	wg.Wait()
	if revsStatus == lookupPending || idsStatus == lookupPending {
		return pendingResult()
	}

	seen := make(map[string]bool)
	var values []string
	for _, rev := range revs {
		if rev == "" || seen[rev] {
			continue
		}
		seen[rev] = true
		values = append(values, rev)
	}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		values = append(values, id)
	}

	return prefixFilterItems(values, current)
}

func (c *VCSCompleter) completeJJBookmark(ctx context.Context, current string, before []string) Result {
	if len(before) == 0 {
		return Result{}
	}
	switch before[0] {
	case "delete", "move", "rename":
		// Complete with bookmark names (exclude special revisions)
	default:
		return Result{}
	}

	revs, status := c.lookupValues(ctx, "jj_revs", c.listJJRevs)
	if status == lookupPending || status == lookupFailed {
		return sourceResult(nil, status, current)
	}
	var bookmarks []string
	for _, r := range revs {
		if !strings.HasPrefix(r, "@") {
			bookmarks = append(bookmarks, r)
		}
	}
	return prefixFilterItems(bookmarks, current)
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
	result := Result{Items: make([]Item, 0, min(len(values), completionItemLimit))}
	for _, value := range values {
		if prefix == "" || strings.HasPrefix(value, prefix) {
			if !result.add(Item{Value: value, Display: value}) {
				break
			}
		}
	}
	return result
}

// runGit runs a git query in dir.
func runGit(ctx context.Context, dir string, args ...string) ([]string, error) {
	return runIsolatedCommandIn(ctx, dir, "git", gitArgs(args...)...)
}

// gitArgs prefixes a git query's arguments with the options every
// completion query runs with. It must not write to the repository:
// completion runs while the user's own git commands do and is killed at its
// deadline, so it must not take the index lock to refresh the index as git
// status would (--no-optional-locks). And a Tab must not run programs the
// repository's own config names: core.fsmonitor is off. Clean and
// textconv filters a repository configures can still run when git status
// compares a modified file; that is git's own trust model, the same
// exposure as a prompt that shows git status.
func gitArgs(args ...string) []string {
	return append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false"}, args...)
}

// runJJ runs a jj query in dir without recording an operation: every jj
// command snapshots the working copy (and imports a colocated git repo's
// refs) unless told to ignore it, and merges divergent operation heads
// unless loaded at one; while heads diverge, the query fails instead.
func runJJ(ctx context.Context, dir string, args ...string) ([]string, error) {
	return runIsolatedCommandIn(ctx, dir, "jj", append([]string{"--ignore-working-copy", "--at-op", "@", "--color", "never"}, args...)...)
}

func defaultListGitRefs(ctx context.Context, dir string) ([]string, error) {
	lines, err := runGit(ctx, dir, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes", "refs/tags")
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

func defaultListJJRevs(ctx context.Context, dir string) ([]string, error) {
	lines, err := runJJ(ctx, dir, "bookmark", "list", "-T", `name ++ "\n"`)
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

// defaultListGitModified lists the changed, staged and untracked files of
// the repository, relative to dir, where git add will run. git status
// (porcelain, -z) reports paths from the repository root and, without the
// index lock, refreshes stat data in memory only; git diff would write the
// refreshed index.
func defaultListGitModified(ctx context.Context, dir string) ([]string, error) {
	prefix, err := runGit(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	out, err := isolatedCommandOutput(ctx, dir, "git", gitArgs("status", "-z", "--untracked-files=all")...)
	if err != nil {
		return nil, err
	}
	base := ""
	if len(prefix) > 0 {
		base = prefix[0]
	}
	return statusPaths(out, base), nil
}

// statusPaths returns the paths of git status -z output, relative to base,
// the directory's path from the repository root: each record is "XY path",
// and a rename or copy, in the index (X) or the work tree (Y), is followed
// by a record with its source, which is gone.
func statusPaths(out, base string) []string {
	var files []string
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		if len(record) < 4 {
			continue
		}
		if strings.ContainsAny(record[:2], "RC") {
			i++ // the source path
		}
		path, err := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(record[3:]))
		if err != nil {
			continue
		}
		files = append(files, path)
	}
	sort.Strings(files)
	return files
}

func defaultListGitStashes(ctx context.Context, dir string) ([]string, error) {
	lines, err := runGit(ctx, dir, "stash", "list", "--format=%gd")
	if err != nil {
		return nil, err
	}

	var stashes []string
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s != "" {
			stashes = append(stashes, s)
		}
	}
	return stashes, nil
}

func defaultListGitRemotes(ctx context.Context, dir string) ([]string, error) {
	lines, err := runGit(ctx, dir, "remote")
	if err != nil {
		return nil, err
	}

	var remotes []string
	for _, line := range lines {
		r := strings.TrimSpace(line)
		if r != "" {
			remotes = append(remotes, r)
		}
	}
	return remotes, nil
}

// jjChangeIDLimit bounds the change ids listed, newest first: all() in a
// large repository is far more than a menu shows and would outlast the
// query's timeout.
const jjChangeIDLimit = 1000

func defaultListJJChangeIDs(ctx context.Context, dir string) ([]string, error) {
	lines, err := runJJ(ctx, dir, "log", "-T", `change_id.short() ++ "\n"`, "--no-graph", "-r", "all()", "--limit", strconv.Itoa(jjChangeIDLimit))
	if err != nil {
		return nil, err
	}

	var ids []string
	seen := make(map[string]bool)
	for _, line := range lines {
		id := strings.TrimSpace(line)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func runIsolatedCommand(ctx context.Context, command string, args ...string) ([]string, error) {
	return runIsolatedCommandIn(ctx, "", command, args...)
}

// runIsolatedCommandIn runs the command in dir ("" = inherit the process
// working directory) and returns its output lines.
func runIsolatedCommandIn(ctx context.Context, dir, command string, args ...string) ([]string, error) {
	out, err := isolatedCommandOutput(ctx, dir, command, args...)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimRight(out, "\n")
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

// isolatedCommandOutput runs the command in dir with no input and returns
// its standard output.
func isolatedCommandOutput(ctx context.Context, dir, command string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	configureIsolatedCompletionCommand(cmd)

	devNull, err := os.Open(os.DevNull)
	if err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
