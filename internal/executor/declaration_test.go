package executor

import (
	"bytes"
	"context"
	"os"
	"testing"
)

// A declaration builtin whose name is escaped or quoted (conda's init
// calls `\local`), or that builtin or command names, runs as the builtin,
// its arguments expanded and split as a command's are, as in bash 5.2
// (checked in docker): the interpreter parses only a bare name as a
// declaration, and failed the others as "local: unsupported builtin".
func TestExecute_EscapedDeclarationBuiltins(t *testing.T) {
	tests := []struct{ name, line, out string }{
		{"\\local", `f() { \local x=1; echo "[$x]"; }; f; echo "[$x]"`, "[1]\n[]\n"},
		{"quoted local", `f() { "local" x=1; echo "[$x]"; }; f`, "[1]\n"},
		{"\\local splits", `v="a b"; f() { \local x=$v; echo "[$x]"; declare -p b >/dev/null && echo declared; }; f`, "[a]\ndeclared\n"},
		{"\\export", `\export DECL_TEST_Y=2; /usr/bin/printenv DECL_TEST_Y`, "2\n"},
		{"\\declare -x", `\declare -x DECL_TEST_Z=3; /usr/bin/printenv DECL_TEST_Z`, "3\n"},
		{"\\typeset", `\typeset w=4; echo $w`, "4\n"},
		{"\\readonly", `\readonly r=5; echo $r; declare -p r`, "5\ndeclare -r r=\"5\"\n"},
		{"builtin local", `f() { builtin local x=1; echo "[$x]"; }; f; echo "[$x]"`, "[1]\n[]\n"},
		{"command export", `command export DECL_TEST_Q=7; /usr/bin/printenv DECL_TEST_Q`, "7\n"},
		{"expanded name", `d=local; f() { $d x=8; echo "[$x]"; }; f; echo "[$x]"`, "[8]\n[]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range []string{"DECL_TEST_Y", "DECL_TEST_Z", "DECL_TEST_Q"} {
				t.Setenv(name, "") // restored as the test ends: hash exports into its own environment
				os.Unsetenv(name)
			}
			e := New()
			var stdout, stderr bytes.Buffer
			if _, err := e.Execute(context.Background(), tt.line, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != tt.out || stderr.Len() > 0 {
				t.Errorf("%s:\nstdout %q, want %q\nstderr %q", tt.line, stdout.String(), tt.out, stderr.String())
			}
		})
	}
}
