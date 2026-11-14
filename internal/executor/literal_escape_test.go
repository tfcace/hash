package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// A backslash outside quotes quotes the character after it, and goes, in
// every word the shell takes as one string: an assignment's value, a
// redirect's file, a here-string, the word a case tests, a [[ operand that
// is no pattern, and the word of ${x:-word} and ${x/p/word}. The
// interpreter dropped it only in a command's arguments, so `dir=My\
// Documents` held the backslash, `> a\ b` wrote a file named a\ b, and
// `[[ a\ b == "a b" ]]` was false. In double quotes it stays, as in bash.
func TestExecute_BackslashQuotesInAOneStringWord(t *testing.T) {
	keepEnv(t, "v") // export v reaches the process's environment
	for _, tc := range []struct{ line, want string }{
		{`v=a\ b; echo "$v"`, "a b"},
		{`v=a\(b\)\|\*\$x\\\'\~; echo "$v"`, `a(b)|*$x\'~`},
		{`v=\~; echo "$v"`, "~"},
		{`export v=a\ b; echo "$v"`, "a b"},
		{`f() { local v=a\ b; echo "$v"; }; f`, "a b"},
		{`arr=([1]=a\ b); echo "${arr[1]}"`, "a b"},
		{`v=a\ b sh -c 'echo "$v"'`, "a b"},
		{`cat <<< a\ b`, "a b"},
		{`case a\ b in "a b") echo T;; *) echo F;; esac`, "T"},
		{`[[ a\ b == "a b" ]] && echo T || echo F`, "T"},
		{`[[ \* == "*" ]] && echo T || echo F`, "T"},
		{`[[ "a b" == a\ * ]] && echo T || echo F`, "T"},
		{`[[ ab == a\* ]] && echo T || echo F`, "F"},
		{`[[ a\ b < a\ c ]] && echo T || echo F`, "T"},
		{`echo ${x:-a\ b}`, "a b"},
		{`x=${y:-a\ b}; echo "$x"`, "a b"},
		{`echo "${x:-a\ b}"`, `a\ b`},
		{`echo "${x:-${y:-a\ b}}"`, `a\ b`},
		{`v=a; echo ${v/a/x\ y}`, "x y"},
		{`v="a\ b"; echo "$v"`, `a\ b`},
		{`v=a\
b; echo "$v"`, "ab"},
		{`echo a\ b`, "a b"},
		{"cat <<\\E\n$HOME\nE", "$HOME"},
	} {
		var out bytes.Buffer
		if _, err := New().Execute(context.Background(), tc.line, &out, io.Discard); err != nil {
			t.Fatalf("%s: %v", tc.line, err)
		}
		if got := out.String(); got != tc.want+"\n" {
			t.Errorf("%s: got %q, want %q", tc.line, got, tc.want)
		}
	}
}

// A redirect's file is the word without its quoting backslashes.
func TestExecute_BackslashQuotesInARedirectFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var out bytes.Buffer
	if _, err := New().Execute(context.Background(), `echo q > a\ b; cat a\ b`, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a b")); err != nil || out.String() != "q\n" {
		t.Errorf("file a b: %v; cat printed %q", err, out.String())
	}
}

// A heredoc's body keeps the backslash in ${x:-word}, as double quotes do.
func TestExecute_BackslashStaysInAHeredocExpansion(t *testing.T) {
	var out bytes.Buffer
	if _, err := New().Execute(context.Background(), "cat <<E\n${x:-a\\ b}\nE", &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if out.String() != "a\\ b\n" {
		t.Errorf("got %q, want a\\ b", out.String())
	}
}

// keepEnv gives the process's environment variables names back as they
// were once t ends. An executor's exports are the process's own (it is
// the one shell of hash's process), and each New starts from them: a
// test's export would reach every executor after it.
func keepEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		was, set := os.LookupEnv(name)
		t.Cleanup(func() {
			if set {
				_ = os.Setenv(name, was)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}
