package executor

import (
	"context"
	"strings"
	"testing"
	"time"
)

// declare -f prints a function as it was written: none of the calls hash
// runs its jobs and its substitutions through, nor the array an
// interactive shell reads $- from, so what it prints defines the same
// function in bash, and in hash again.
func TestExecute_DeclareFPrintsAFunctionAsWritten(t *testing.T) {
	tests := []struct{ name, define, want string }{
		{"a job", "f() {\n\tsleep 0.1 &\n\techo x\n}", "f()\n{\n\tsleep 0.1 &\n\techo x\n}\n"},
		{"a job of several statements, redirected", "f() {\n\t{\n\t\techo a\n\t\techo 'b c'\n\t} >/dev/null 2>&1 &\n}",
			"f()\n{\n\t{\n\t\techo a\n\t\techo 'b c'\n\t} >/dev/null 2>&1 &\n}\n"},
		{"a job in a loop", "f() {\n\tfor i in 1 2; do\n\t\tsleep \"$i\" &\n\tdone\n\twait\n}",
			"f()\n{\n\tfor i in 1 2; do\n\t\tsleep \"$i\" &\n\tdone\n\twait\n}\n"},
		{"a job that reads a here-document", "f() {\n\tcat <<EOF &\nit's $HOME\nEOF\n\twait\n}", "f()\n{\n\tcat <<EOF &\nit's $HOME\nEOF\n\twait\n}\n"},
		{"a substitution that starts a job", "f() {\n\tx=$(\n\t\tsleep 0.1 &\n\t\techo now\n\t)\n}", "f()\n{\n\tx=$(\n\t\tsleep 0.1 &\n\t\techo now\n\t)\n}\n"},
		{"the shell's flags", "f() {\n\techo $- ${-#*i} \"${#-}\"\n}", "f()\n{\n\techo $- ${-#*i} \"${#-}\"\n}\n"},
		{"jobs on one line", `f() { sleep 0.1 & x=$(sleep 0.1 & echo now); { echo a; echo b; } & }`, ""},
		{"no rewrite", `f() { echo hi | tr a-z A-Z; }`, ""},
	}
	for _, tc := range tests {
		for _, mode := range []terminalMode{terminalPlain, terminalInteractive} {
			e := New()
			e.terminal = mode
			run := func(line string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var out, errb strings.Builder
				if _, err := e.Execute(ctx, line, &out, &errb); err != nil || errb.Len() > 0 {
					t.Fatalf("%s: %q: err %v, stderr %q", tc.name, line, err, errb.String())
				}
				return out.String()
			}
			run(tc.define)
			got := run(`declare -f f`)
			if strings.Contains(got, "__hash") {
				t.Errorf("%s: declare -f shows hash's own calls:\n%s", tc.name, got)
			}
			if tc.want != "" && got != tc.want {
				t.Errorf("%s: declare -f printed\n%s\nwant\n%s", tc.name, got, tc.want)
			}
			for _, again := range []string{`typeset -f f`, `\declare -f f`, `builtin declare -f f`, `eval "$(declare -f f)"; declare -f f`} {
				if out := run(again); out != got {
					t.Errorf("%s: %s printed\n%s\nwant what declare -f f did\n%s", tc.name, again, out, got)
				}
			}
		}
	}
}

// declare -f prints each function it names and fails with 1 when one is
// none, silently, as bash does; a function that prints its own source
// (`declare -f` inside it) shows that call as it was written.
func TestExecute_DeclareFNamesSeveralFunctions(t *testing.T) {
	runLines(t, []struct{ line, want string }{
		{"f() {\n:\n}\ng() {\ntrue &\n}\ndeclare -f f g; echo \"st=$?\"", "f()\n{\n\t:\n}\ng()\n{\n\ttrue &\n}\nst=0\n"},
		{"f() {\n:\n}\ndeclare -f nope f; echo \"st=$?\"", "f()\n{\n\t:\n}\nst=1\n"},
		{"f() {\ndeclare -f f\n}\nf", "f()\n{\n\tdeclare -f f\n}\n"},
		{`f() { sleep 0.01 & wait; echo ran; }; eval "$(declare -f f)"; f`, "ran\n"},
	})
}
