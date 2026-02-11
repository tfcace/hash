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
