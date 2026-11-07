package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// eval, source and alias bodies run in place in the calling shell, as in
// bash: they do not fire the EXIT trap, the body's status, exit
// and return act on the caller, and $0 stays the shell's.
func TestExecutor_NestedRunsKeepTheCallersControlFlow(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ret5 := write("ret5.sh", "return 5\necho not-reached\n")
	guard := write("guard.sh", "[ -z \"$NOT_SET_HASH_PS1\" ] && return\necho not-reached\n")
	params := write("params.sh", "echo \"[$1 $2]\"\n")
	tests := []struct {
		name, line, want string
	}{
		{"eval does not fire the EXIT trap", `trap 'echo TRAP' EXIT; eval ':'; echo after`, "after\n"},
		{"source does not fire the EXIT trap", `trap 'echo TRAP' EXIT; source /dev/null; echo after`, "after\n"},
		{"alias does not fire the EXIT trap", `trap 'echo TRAP' EXIT; alias q=echo; echo after`, "after\n"},
		{"eval keeps the status", `eval 'sh -c "exit 2"'; echo "rc=$?"`, "rc=2\n"},
		{"source keeps the status", `source ` + ret5 + `; echo "rc=$?"`, "rc=5\n"},
		{"top-level return ends a sourced file", `source ` + guard + `; echo "rc=$?"`, "rc=0\n"},
		{"source passes its arguments", `source ` + params + ` a b`, "[a b]\n"},
		{"return in eval returns from the function", `f() { eval 'return 3'; echo after; }; f; echo "rc=$?"`, "rc=3\n"},
		{"exit in eval ends the subshell", `( eval 'exit 4'; echo inner ); echo "rc=$?"`, "rc=4\n"},
		{"eval keeps $0", `eval ':'; echo "$0"`, "hash\n"},
		{"source keeps $0", `source /dev/null; echo "$0"`, "hash\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New()
			var stdout, stderr bytes.Buffer
			_, _ = e.Execute(context.Background(), tt.line, &stdout, &stderr)
			if stdout.String() != tt.want {
				t.Fatalf("stdout = %q, want %q (stderr %q)", stdout.String(), tt.want, stderr.String())
			}
		})
	}
}

// A zsh-dialect body bash cannot express still runs, on the calling runner.
func TestExecutor_ZshOnlyNestedBodyStillRuns(t *testing.T) {
	e := New()
	if err := e.SetDialect("zsh"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	_, _ = e.Execute(context.Background(), `arr=(a b); eval 'y=${(j:,:)arr}; echo done'; ( eval 'z=1' ); echo "[$z]"`, &stdout, &stderr)
	if stdout.String() != "done\n[]\n" {
		t.Fatalf("stdout = %q, want done and [] (stderr %q)", stdout.String(), stderr.String())
	}
}

// A syntax error in eval or a sourced file is reported, with status 2, as in
// bash, and a sourced file's statements before it run; only the shell's
// startup files skip an eval's quietly, so a config line written for
// another shell does not stop the shell, and load none of a sourced
// file's, leaving it to the shell to say (StartupResult.NotLoaded).
func TestExecutor_EvalAndSourceSyntaxErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.sh")
	if err := os.WriteFile(bad, []byte("echo in\nif then\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ line, stdout, stderr, startup string }{
		{`eval 'if then'; echo "rc=$?"`, "rc=2\n", "hash: eval: line 1: `if` must be followed by a statement list\n", "rc=0\n"},
		{`source ` + bad + `; echo "rc=$?"`, "in\nrc=2\n", "hash: " + bad + ": line 2: `if` must be followed by a statement list\n", "rc=2\n"},
	} {
		e := New()
		var out, errOut bytes.Buffer
		if _, err := e.Execute(context.Background(), tt.line, &out, &errOut); err != nil {
			t.Fatal(err)
		}
		if out.String() != tt.stdout || errOut.String() != tt.stderr {
			t.Errorf("%s: stdout %q, stderr %q; want %q and %q", tt.line, out.String(), errOut.String(), tt.stdout, tt.stderr)
		}

		e = New()
		out.Reset()
		errOut.Reset()
		if _, err := e.ExecuteStartup(context.Background(), "", "", tt.line, &out, &errOut); err != nil {
			t.Fatal(err)
		}
		if out.String() != tt.startup || errOut.String() != "" {
			t.Errorf("startup %s: stdout %q, stderr %q; want %q and nothing", tt.line, out.String(), errOut.String(), tt.startup)
		}
	}
}
