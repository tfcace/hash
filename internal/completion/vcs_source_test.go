package completion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// installFakeTool puts an executable shell script named name first on PATH.
func installFakeTool(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// tabUntil presses Tab (a 150ms completion budget, like the editor) until
// want shows up or the deadline passes, and returns the last result.
func tabUntil(t *testing.T, c Completer, line string, want string, deadline time.Duration) (Result, bool) {
	t.Helper()
	var result Result
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		result, _ = c.Complete(ctx, line, len(line))
		cancel()
		for _, item := range result.Items {
			if item.Value == want {
				return result, true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return result, false
}

// In a repository where `git for-each-ref` takes longer than one Tab's
// budget, refs must still show up once the query finishes, instead of the
// timed-out query being cached as "no refs".
func TestVCSCompleter_SlowRefQueryEventuallyAnswers(t *testing.T) {
	installFakeTool(t, "git", `while case "$1" in -c) shift ;; --no-optional-locks) ;; *) false ;; esac; do shift; done
case "$1" in
for-each-ref) sleep 0.25; echo main ;;
esac
`)
	c := NewVCSCompleter()
	if _, ok := tabUntil(t, c, "git checkout ", "main", 1500*time.Millisecond); !ok {
		t.Fatal("refs from a slow git for-each-ref never appeared")
	}
}

// A failing git query must not be cached as an authoritative partial list.
func TestVCSCompleter_GitAddDoesNotAnswerWithPartialListWhenAQueryFails(t *testing.T) {
	installFakeTool(t, "git", `while case "$1" in -c) shift ;; --no-optional-locks) ;; *) false ;; esac; do shift; done
case "$1" in
rev-parse) echo ;;
status) printf ' M modified.go\0'; exit 1 ;;
esac
`)
	c := NewVCSCompleter()
	result, _ := tabUntil(t, c, "git add ", "never", 400*time.Millisecond)
	if len(result.Items) != 0 {
		t.Fatalf("git add answered %#v from a partly failed query, want fall-through", result.Items)
	}
}

// A lookup that joins a query started by a Tab whose deadline already
// passed must get the query's answer, not that Tab's cancellation, and the
// cancellation must not be cached as "no refs".
func TestVCSCompleter_JoinerDoesNotInheritFirstCallersCancellation(t *testing.T) {
	c := NewVCSCompleter()
	c.listGitRefs = func(ctx context.Context, dir string) ([]string, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(40 * time.Millisecond):
			return []string{"main"}, nil
		}
	}

	first, cancel := context.WithCancel(context.Background())
	go func() { _, _ = c.Complete(first, "git checkout ", len("git checkout ")) }()
	time.Sleep(5 * time.Millisecond)
	joined := make(chan Result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		r, _ := c.Complete(ctx, "git checkout ", len("git checkout "))
		joined <- r
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()

	if r := <-joined; r.Outcome() == OutcomeNotMine {
		t.Error("joiner got nothing: it inherited the first caller's cancellation")
	}
	if _, ok := tabUntil(t, c, "git checkout ", "main", 500*time.Millisecond); !ok {
		t.Fatal("refs never appeared after the first caller was canceled")
	}
}

// Queries run in the directory the Tab was pressed in, passed explicitly,
// and results are kept per directory.
func TestVCSCompleter_QueriesRunInLookupDirectory(t *testing.T) {
	c := NewVCSCompleter()
	dir := "/repo/a"
	c.getwd = func() string { return dir }
	var gotDirs []string
	c.listGitRefs = func(ctx context.Context, d string) ([]string, error) {
		gotDirs = append(gotDirs, d)
		return []string{filepath.Base(d)}, nil
	}

	line := "git checkout "
	a, _ := c.Complete(context.Background(), line, len(line))
	dir = "/repo/b"
	b, _ := c.Complete(context.Background(), line, len(line))

	if len(gotDirs) != 2 || gotDirs[0] != "/repo/a" || gotDirs[1] != "/repo/b" {
		t.Fatalf("queries ran in %v, want [/repo/a /repo/b]", gotDirs)
	}
	if len(a.Items) != 1 || a.Items[0].Value != "a" || len(b.Items) != 1 || b.Items[0].Value != "b" {
		t.Fatalf("results leaked across directories: a=%v b=%v", a.Items, b.Items)
	}
}

// A query slower than the Tab's wait claims the argument as pending (no
// filenames) and signals ready when it lands.
func TestVCSCompleter_SlowQueryIsPendingThenReady(t *testing.T) {
	c := NewVCSCompleter()
	release := make(chan struct{})
	c.listGitRefs = func(ctx context.Context, dir string) ([]string, error) {
		<-release
		return []string{"main"}, nil
	}
	ready := make(chan struct{}, 1)
	c.SetOnReady(func() { ready <- struct{}{} })

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	result, _ := c.Complete(ctx, "git checkout ", len("git checkout "))
	if result.Outcome() != OutcomePending {
		t.Fatalf("outcome = %v, want pending", result.Outcome())
	}
	close(release)
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("no ready notification")
	}
}
