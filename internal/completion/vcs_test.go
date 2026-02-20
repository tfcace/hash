package completion

import (
	"context"
	"errors"
	"testing"
)

func TestVCSCompleter_Name(t *testing.T) {
	c := NewVCSCompleter()
	if c.Name() != "vcs" {
		t.Fatalf("Name() = %q, want %q", c.Name(), "vcs")
	}
}

func TestVCSCompleter_GitCheckout(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitRefs = func(ctx context.Context) ([]string, error) {
		return []string{"main", "feature/api", "origin/main"}, nil
	}

	result, err := c.Complete(context.Background(), "git checkout fe", len("git checkout fe"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "feature/api" {
		t.Fatalf("expected feature/api, got %q", result.Items[0].Value)
	}
}

func TestVCSCompleter_GitSwitch_AfterCreateFlag(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitRefs = func(ctx context.Context) ([]string, error) {
		t.Fatalf("should not query refs when creating a new branch")
		return nil, nil
	}

	result, err := c.Complete(context.Background(), "git switch -c feat", len("git switch -c feat"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no items, got %d", len(result.Items))
	}
}

func TestVCSCompleter_GitCheckout_AfterDoubleDash(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitRefs = func(ctx context.Context) ([]string, error) {
		t.Fatalf("should not query refs for pathspec context")
		return nil, nil
	}

	result, err := c.Complete(context.Background(), "git checkout -- sr", len("git checkout -- sr"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no items, got %d", len(result.Items))
	}
}

func TestVCSCompleter_JJEdit(t *testing.T) {
	c := NewVCSCompleter()
	c.listJJRevs = func(ctx context.Context) ([]string, error) {
		return []string{"@", "@-", "main", "tools"}, nil
	}

	result, err := c.Complete(context.Background(), "jj edit to", len("jj edit to"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "tools" {
		t.Fatalf("expected tools, got %q", result.Items[0].Value)
	}
}

func TestVCSCompleter_JJOnlyFirstPositional(t *testing.T) {
	c := NewVCSCompleter()
	c.listJJRevs = func(ctx context.Context) ([]string, error) {
		t.Fatalf("should not query refs for second positional argument")
		return nil, nil
	}

	result, err := c.Complete(context.Background(), "jj new main to", len("jj new main to"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no items, got %d", len(result.Items))
	}
}

func TestVCSCompleter_IgnoreLookupErrors(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitRefs = func(ctx context.Context) ([]string, error) {
		return nil, errors.New("not a git repo")
	}

	result, err := c.Complete(context.Background(), "git checkout ", len("git checkout "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no items on lookup error, got %d", len(result.Items))
	}
}

func TestVCSCompleter_GitAdd_ModifiedFiles(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitModified = func(ctx context.Context) ([]string, error) {
		return []string{"src/main.go", "src/util.go", "README.md"}, nil
	}

	result, err := c.Complete(context.Background(), "git add src", len("git add src"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 items matching 'src', got %d: %+v", len(result.Items), result.Items)
	}
}

func TestVCSCompleter_GitBranchDelete_CompleteBranches(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitRefs = func(ctx context.Context) ([]string, error) {
		return []string{"main", "feature/api", "bugfix/login"}, nil
	}

	result, err := c.Complete(context.Background(), "git branch -d fe", len("git branch -d fe"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "feature/api" {
		t.Errorf("got %q, want %q", result.Items[0].Value, "feature/api")
	}
}

func TestVCSCompleter_GitStashPop_CompleteEntries(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitStashes = func(ctx context.Context) ([]string, error) {
		return []string{"stash@{0}", "stash@{1}", "stash@{2}"}, nil
	}

	result, err := c.Complete(context.Background(), "git stash pop ", len("git stash pop "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(result.Items))
	}
}

func TestVCSCompleter_GitRemote_CompleteNames(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitRemotes = func(ctx context.Context) ([]string, error) {
		return []string{"origin", "upstream"}, nil
	}

	result, err := c.Complete(context.Background(), "git remote remove ", len("git remote remove "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result.Items))
	}
}

func TestVCSCompleter_GitLog_CompleteRefs(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitRefs = func(ctx context.Context) ([]string, error) {
		return []string{"main", "develop"}, nil
	}

	result, err := c.Complete(context.Background(), "git log ", len("git log "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result.Items))
	}
}

func TestVCSCompleter_JJBookmarkDelete_CompleteBookmarks(t *testing.T) {
	c := NewVCSCompleter()
	c.listJJRevs = func(ctx context.Context) ([]string, error) {
		return []string{"@", "@-", "@+", "main", "feature"}, nil
	}

	result, err := c.Complete(context.Background(), "jj bookmark delete ", len("jj bookmark delete "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	// Should only show non-@ revisions (bookmarks)
	for _, item := range result.Items {
		if item.Value == "@" || item.Value == "@-" || item.Value == "@+" {
			t.Errorf("should not show special revision %q", item.Value)
		}
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 bookmark items, got %d: %+v", len(result.Items), result.Items)
	}
}

func TestVCSCompleter_JJDescribe_CompleteChangeIDs(t *testing.T) {
	c := NewVCSCompleter()
	c.listJJChangeIDs = func(ctx context.Context) ([]string, error) {
		return []string{"abc123", "def456", "ghi789"}, nil
	}

	result, err := c.Complete(context.Background(), "jj describe abc", len("jj describe abc"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "abc123" {
		t.Errorf("got %q, want %q", result.Items[0].Value, "abc123")
	}
}
