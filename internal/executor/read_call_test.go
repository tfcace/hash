package executor

import (
	"reflect"
	"testing"
)

// parseRead takes read's options apart as the builtin does, keeping the
// call without its prompt, and gives up on an option the builtin rejects.
func TestParseRead(t *testing.T) {
	tests := []struct {
		args   []string
		want   readArgs
		wantOK bool
	}{
		{[]string{"read", "x"}, readArgs{args: []string{"read", "--", "x"}}, true},
		{[]string{"read", "-p", "Name: ", "n"}, readArgs{prompt: "Name: ", args: []string{"read", "--", "n"}}, true},
		{[]string{"read", "-rsp", "Pw: ", "pw"}, readArgs{prompt: "Pw: ", silent: true, args: []string{"read", "-r", "-s", "--", "pw"}}, true},
		{[]string{"read", "-s", "-a", "arr"}, readArgs{silent: true, args: []string{"read", "-s", "-a", "--", "arr"}}, true},
		{[]string{"read", "--", "-x"}, readArgs{args: []string{"read", "--", "-x"}}, true},
		{[]string{"read", "-p"}, readArgs{}, false},
		{[]string{"read", "-p", ""}, readArgs{}, false},
		{[]string{"read", "-t", "1", "x"}, readArgs{}, false},
		{[]string{"read", "+r"}, readArgs{}, false},
		{[]string{"read", "-"}, readArgs{}, false},
	}
	for _, tt := range tests {
		got, ok := parseRead(tt.args)
		if ok != tt.wantOK || (ok && !reflect.DeepEqual(got, tt.want)) {
			t.Errorf("parseRead(%q) = %+v, %v; want %+v, %v", tt.args, got, ok, tt.want, tt.wantOK)
		}
	}
}
