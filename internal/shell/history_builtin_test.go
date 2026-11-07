package shell

import (
	"strings"
	"testing"
)

// `history N` lists the last N lines, numbered as in the whole list, as
// bash's does; it searched history for "N".
func TestHistory_NumberListsTheLastLines(t *testing.T) {
	sh := newHistoryShell(t, "echo 3 apples", "ls", "pwd", "date")
	for _, tc := range []struct{ arg, want string }{
		{"2", "    3  pwd\n    4  date\n"},
		{"9", "    1  echo 3 apples\n    2  ls\n    3  pwd\n    4  date\n"},
		{"0", ""},
	} {
		var out strings.Builder
		if err := sh.builtinHistory(&out, []string{tc.arg}); err != nil {
			t.Fatal(err)
		}
		if out.String() != tc.want {
			t.Errorf("history %s printed %q, want %q", tc.arg, out.String(), tc.want)
		}
	}
}
