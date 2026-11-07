package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/expand"
)

// TestIsCommandNotFound tests the IsCommandNotFound function.
func TestIsCommandNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"command not found", &CommandNotFoundError{Command: "foo"}, true},
		{"other error", &testError{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsCommandNotFound(tt.err)
			if got != tt.want {
				t.Errorf("IsCommandNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsCommandNotFound_Positive tests positive case for IsCommandNotFound.
func TestIsCommandNotFound_Positive(t *testing.T) {
	err := &CommandNotFoundError{Command: "nonexistent"}
	if !IsCommandNotFound(err) {
		t.Error("IsCommandNotFound should return true for CommandNotFoundError")
	}
}

type testError struct{}

func (e *testError) Error() string { return "test error" }

// TestCommandNotFoundError_Error tests the Error method.
func TestCommandNotFoundError_Error(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"foo", "foo: command not found"},
		{"bar", "bar: command not found"},
		{"", ": command not found"},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			err := &CommandNotFoundError{Command: tt.cmd}
			if err.Error() != tt.want {
				t.Errorf("Error() = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

// TestLimitedWriter tests the limitedWriter functionality.
func TestLimitedWriter(t *testing.T) {
	t.Run("under limit", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLimitedWriter(&buf, 100)

		n, err := lw.Write([]byte("hello"))
		if err != nil {
			t.Errorf("Write() error = %v", err)
		}
		if n != 5 {
			t.Errorf("Write() = %d, want 5", n)
		}
		if lw.WasTruncated() {
			t.Error("WasTruncated() should be false")
		}
		if buf.String() != "hello" {
			t.Errorf("buffer = %q, want %q", buf.String(), "hello")
		}
	})

	t.Run("at limit", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLimitedWriter(&buf, 5)

		n, err := lw.Write([]byte("hello"))
		if err != nil {
			t.Errorf("Write() error = %v", err)
		}
		if n != 5 {
			t.Errorf("Write() = %d, want 5", n)
		}
		if lw.WasTruncated() {
			t.Error("WasTruncated() should be false at exact limit")
		}
	})

	t.Run("over limit", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLimitedWriter(&buf, 3)

		n, err := lw.Write([]byte("hello"))
		if err != nil {
			t.Errorf("Write() error = %v", err)
		}
		if n != 5 {
			t.Errorf("Write() = %d, want 5 (reports original size)", n)
		}
		if !lw.WasTruncated() {
			t.Error("WasTruncated() should be true")
		}
		if buf.String() != "hel" {
			t.Errorf("buffer = %q, want %q", buf.String(), "hel")
		}
	})

	t.Run("multiple writes over limit", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLimitedWriter(&buf, 5)

		lw.Write([]byte("hel"))
		lw.Write([]byte("lo world"))

		if !lw.WasTruncated() {
			t.Error("WasTruncated() should be true")
		}
		if buf.String() != "hello" {
			t.Errorf("buffer = %q, want %q", buf.String(), "hello")
		}
	})

	t.Run("write after exhausted", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLimitedWriter(&buf, 3)

		lw.Write([]byte("hello"))
		n, err := lw.Write([]byte("more"))

		if err != nil {
			t.Errorf("Write() error = %v", err)
		}
		if n != 4 {
			t.Errorf("Write() = %d, want 4", n)
		}
		if buf.String() != "hel" {
			t.Errorf("buffer = %q, want %q", buf.String(), "hel")
		}
	})

	t.Run("OriginalSize", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLimitedWriter(&buf, 5)

		lw.Write([]byte("hello"))
		lw.Write([]byte(" world"))

		if lw.OriginalSize() != 11 {
			t.Errorf("OriginalSize() = %d, want 11", lw.OriginalSize())
		}
	})

	t.Run("LimitSize", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLimitedWriter(&buf, 42)

		if lw.LimitSize() != 42 {
			t.Errorf("LimitSize() = %d, want 42", lw.LimitSize())
		}
	})
}

// TestEnvStore tests the envStore functionality.
func TestEnvStore(t *testing.T) {
	t.Run("Get from nil store", func(t *testing.T) {
		var store *envStore
		v := store.Get("PATH")
		if v.Set {
			t.Error("Get from nil store should return unset variable")
		}
	})

	t.Run("Get nonexistent", func(t *testing.T) {
		store := &envStore{vars: make(map[string]expand.Variable)}
		v := store.Get("NONEXISTENT")
		if v.Set {
			t.Error("Get nonexistent should return unset variable")
		}
	})

	t.Run("Get existing", func(t *testing.T) {
		store := &envStore{vars: map[string]expand.Variable{
			"FOO": {Set: true, Str: "bar"},
		}}
		v := store.Get("FOO")
		if !v.Set || v.Str != "bar" {
			t.Errorf("Get(FOO) = %v, want {Set: true, Str: bar}", v)
		}
	})

	t.Run("Each on nil store", func(t *testing.T) {
		var store *envStore
		count := 0
		store.Each(func(name string, vr expand.Variable) bool {
			count++
			return true
		})
		if count != 0 {
			t.Errorf("Each on nil store called fn %d times", count)
		}
	})

	t.Run("Each iterates all", func(t *testing.T) {
		store := &envStore{vars: map[string]expand.Variable{
			"A": {Set: true, Str: "1"},
			"B": {Set: true, Str: "2"},
			"C": {Set: true, Str: "3"},
		}}
		count := 0
		store.Each(func(name string, vr expand.Variable) bool {
			count++
			return true
		})
		if count != 3 {
			t.Errorf("Each count = %d, want 3", count)
		}
	})

	t.Run("Each can stop early", func(t *testing.T) {
		store := &envStore{vars: map[string]expand.Variable{
			"A": {Set: true, Str: "1"},
			"B": {Set: true, Str: "2"},
			"C": {Set: true, Str: "3"},
		}}
		count := 0
		store.Each(func(name string, vr expand.Variable) bool {
			count++
			return false // stop after first
		})
		if count != 1 {
			t.Errorf("Each count = %d, want 1", count)
		}
	})

	t.Run("set on nil store", func(t *testing.T) {
		var store *envStore
		// Should not panic
		store.set("FOO", expand.Variable{Set: true, Str: "bar"})
	})

	t.Run("set creates vars map", func(t *testing.T) {
		store := &envStore{}
		store.set("FOO", expand.Variable{Set: true, Str: "bar"})
		if store.vars == nil {
			t.Error("set should create vars map")
		}
		v := store.Get("FOO")
		if !v.Set || v.Str != "bar" {
			t.Errorf("Get after set = %v", v)
		}
	})

	t.Run("replace on nil store", func(t *testing.T) {
		var store *envStore
		// Should not panic
		store.replace(map[string]expand.Variable{
			"FOO": {Set: true, Str: "bar"},
		})
	})

	t.Run("replace overwrites", func(t *testing.T) {
		store := &envStore{vars: map[string]expand.Variable{
			"OLD": {Set: true, Str: "value"},
		}}
		store.replace(map[string]expand.Variable{
			"NEW": {Set: true, Str: "value"},
		})
		if store.Get("OLD").Set {
			t.Error("OLD should not exist after replace")
		}
		if !store.Get("NEW").Set {
			t.Error("NEW should exist after replace")
		}
	})
}

// TestNewEnvStoreFromOS tests creating env store from OS.
func TestNewEnvStoreFromOS(t *testing.T) {
	store := newEnvStoreFromOS()
	if store == nil {
		t.Fatal("newEnvStoreFromOS returned nil")
	}

	// Should have PATH
	path := store.Get("PATH")
	if !path.Set {
		t.Error("PATH should be set")
	}
	if path.Str == "" {
		t.Error("PATH should not be empty")
	}
}

// TestNormalizeEnvName tests environment variable name normalization.
func TestNormalizeEnvName(t *testing.T) {
	// On non-Windows, names should be unchanged
	name := normalizeEnvName("PATH")
	if name != "PATH" {
		t.Errorf("normalizeEnvName(PATH) = %q, want PATH", name)
	}

	name = normalizeEnvName("lower")
	if name != "lower" {
		t.Errorf("normalizeEnvName(lower) = %q, want lower", name)
	}
}

// Positional arguments set after the first line still apply.
func TestExecutor_SetPositionalArgsAppliesAfterFirstLine(t *testing.T) {
	e := New()
	if _, err := e.Execute(context.Background(), `:`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	e.SetPositionalArgs([]string{"script", "a", "b"})
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `echo "$0 $1 $2"`, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "script a b\n" {
		t.Fatalf("stdout = %q, want %q", got, "script a b\n")
	}
}

// A bare return returns the last command's status, as in bash: the
// interpreter's own return returned 0.
func TestExecute_BareReturnKeepsTheLastStatus(t *testing.T) {
	tests := []struct{ line, want string }{
		{`f() { false; return; }; f; echo $?`, "1"},
		{`f() { sh -c 'exit 7'; return; }; f; echo $?`, "7"},
		{`f() { return; }; false; f; echo $?`, "1"},
		{`f() { true; return; }; f; echo $?`, "0"},
		{`f() { return 3; }; f; echo $?`, "3"},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		if _, err := New().Execute(context.Background(), tt.line, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out.String()); got != tt.want {
			t.Errorf("%s: $? = %q, want %s", tt.line, got, tt.want)
		}
	}
}

// A read of a stdin that takes no deadlines (an inherited, blocking pipe)
// ends when the line's context does: hash's fatal signals cancel it. It
// used to block until the pipe had data.
func TestExecute_ReadOfABlockingStdinEndsWithTheContext(t *testing.T) {
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		t.Fatal(err)
	}
	r, w := os.NewFile(uintptr(fds[0]), "stdin"), os.NewFile(uintptr(fds[1]), "writer")
	defer r.Close()
	defer w.Close()
	if r.SetReadDeadline(time.Time{}) == nil {
		t.Skip("the pipe takes deadlines here")
	}
	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = e.Execute(ctx, `read x`, io.Discard, io.Discard)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_, _ = w.WriteString("unblock\n")
		t.Fatal("read did not end with its context")
	}
	// With data the read reads it.
	_, _ = w.WriteString("hello\n")
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `read x; echo "got $x"`, &out, io.Discard); err != nil || out.String() != "got hello\n" {
		t.Fatalf("read with data: %q, %v", out.String(), err)
	}
}

// source takes its path as the shell expanded it, as bash: an unquoted ~
// is the home directory, a quoted "~/x" is the file ~/x, and ~nosuchuser
// stays as it is. It used to expand the tilde again, quoted or not, and
// read ~nosuchuser/x from $HOME/nosuchuser/x.
func TestExecute_SourceTakesThePathAsExpanded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{home, filepath.Join(home, "hash_no_such_user")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "x.sh"), []byte("echo sourced\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(t.TempDir())
	for line, want := range map[string]string{
		`source ~/x.sh; echo "rc=$?"`:                  "sourced\nrc=0\n",
		`source "~/x.sh"; echo "rc=$?"`:                "rc=1\n",
		`source ~hash_no_such_user/x.sh; echo "rc=$?"`: "rc=1\n",
	} {
		var out bytes.Buffer
		if _, err := New().Execute(context.Background(), line, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		if out.String() != want {
			t.Errorf("%s: stdout %q, want %q", line, out.String(), want)
		}
	}
}
