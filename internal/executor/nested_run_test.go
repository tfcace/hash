package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// eval, source and alias run inside the shell that called them: a subshell's
// eval must not change the parent, and a command substitution's eval must
// write into the substitution, not the terminal.
func TestExecutor_NestedRunsUseTheCallingShell(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "set.sh")
	if err := os.WriteFile(script, []byte("y=sourced\necho from-source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, line, want string
	}{
		{"eval in command substitution", `x=$(eval echo hi); echo "[$x]"`, "[hi]\n"},
		{"eval in subshell", `( eval "z=1" ); echo "[$z]"`, "[]\n"},
		{"source in command substitution", `x=$(source ` + script + `); echo "[$x]"`, "[from-source]\n"},
		{"source in subshell", `( source ` + script + ` >/dev/null ); echo "[$y]"`, "[]\n"},
		{"alias in subshell", `( alias zz='echo zz' ); type zz >/dev/null 2>&1 || echo none`, "none\n"},
		{"eval in pipeline", `eval echo piped | tr a-z A-Z`, "PIPED\n"},
		{"top-level eval still persists", `eval "w=2"; echo "[$w]"`, "[2]\n"},
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

// A program bash cannot express (zsh-only syntax in the zsh dialect) runs
// through a nested Runner.Run, which resets the runner's script name: $0
// read "gosh" for the rest of the line.
func TestExecutor_NestedZshRunKeepsDollarZero(t *testing.T) {
	e := New()
	if err := e.SetDialect("zsh"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `x=ab; eval 'echo ${(U)x} >/dev/null; echo "in $0"'; echo "after $0"; echo "sub $(eval 'echo ${(U)x}' >/dev/null; echo $0)"`, &out, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if want := "in hash\nafter hash\nsub hash\n"; out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
	// The nested run sets the caller's EXIT trap aside and puts it back.
	out.Reset()
	if _, err := e.Execute(context.Background(), `(trap 'echo sub' EXIT; eval 'echo ${(U)x}' >/dev/null; trap; trap - EXIT)`, &out, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if want := "trap -- 'echo sub' EXIT\n"; out.String() != want {
		t.Fatalf("subshell trap after a nested run: %q, want %q", out.String(), want)
	}
}

// A parsed source file is handed to the source builtin alone: when source
// is a function, the function runs, and a later plain read of the file gets
// the file, not the text hash printed back for the builtin.
func TestExecutor_SourceFunctionGetsTheFileNotThePendingText(t *testing.T) {
	dir := t.TempDir()
	const body = "echo    spaced    out   # comment\n"
	if err := os.WriteFile(filepath.Join(dir, "fmt.sh"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	e := New()
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `source() { echo "fake $1"; }; source ./fmt.sh; cat < "$PWD/fmt.sh"; unset -f source; source ./fmt.sh`, &out, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if want := "fake ./fmt.sh\n" + body + "spaced out\n"; out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
}

// A program bash cannot express keeps its exit status, as eval does in
// bash: it used to be flattened to 0 or 1.
func TestExecutor_NestedZshRunKeepsItsStatus(t *testing.T) {
	e := New()
	if err := e.SetDialect("zsh"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `x=ab; eval 'echo ${(U)x} >/dev/null; sh -c "exit 4"'; echo "rc=$?"`, &out, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if out.String() != "rc=4\n" {
		t.Fatalf("stdout = %q, want rc=4", out.String())
	}
}
