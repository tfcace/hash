package shell

import (
	"testing"

	"github.com/tfcace/hash/internal/clipboard"
)

// copy counts its entries from 1, the last line's, in its arguments and in
// what it says when one is missing: `copy cmd 3` with one entry said there
// was no command at index 2. An index below 1 names no entry.
func TestCopy_SaysTheIndexTheUserTyped(t *testing.T) {
	sh := &Shell{clipboard: clipboard.NewBuffer(10)}
	sh.clipboard.AddCommand("ls")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"cmd", "3"}, "no command at index 3"},
		{[]string{"out", "2"}, "no command at index 2"},
		{[]string{"all", "5"}, "no entry at index 5"},
		{[]string{"cmd", "0"}, "invalid index: 0 (the last line is 1)"},
		{[]string{"cmd", "-1"}, "invalid index: -1 (the last line is 1)"},
	} {
		err := sh.builtinCopy(tc.args)
		if err == nil || err.Error() != tc.want {
			t.Errorf("copy %v = %v, want %q", tc.args, err, tc.want)
		}
	}
}
