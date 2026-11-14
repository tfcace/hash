package executor

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// Quoted text in a [[ == ]] or case pattern matches itself, as in bash,
// extglob operators included: `[[ '@(x)' == '@(x)' ]]` is true. The
// interpreter escapes only * ? [ \ in quoted text, so @( +( !( in it
// opened an extglob group, and the test was false. Unquoted extglob still
// works, and quoted text keeps its other characters' meaning (none).
func TestExecute_QuotedExtglobTextIsLiteral(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`[[ '@(x)' == '@(x)' ]] && echo T || echo F`, "T"},
		{`v='@(x)'; [[ "$v" == '@(x)' ]] && echo T || echo F`, "T"},
		{`[[ x == '@(x)' ]] && echo T || echo F`, "F"},
		{`[[ '+(a)' == "+(a)" ]] && echo T || echo F`, "T"},
		{`[[ a == '!(b)' ]] && echo T || echo F`, "F"},
		{`[[ '!(b)' != '!(b)' ]] && echo T || echo F`, "F"},
		{`[[ 'a@(x)b' == a'@(x)'* ]] && echo T || echo F`, "T"},
		{`v=x; [[ '@(x)' == "@($v)" ]] && echo T || echo F`, "T"},
		{`[[ '@(x)$' == "@(x)\$" ]] && echo T || echo F`, "T"},
		{`[[ '~(x)' == '~(x)' ]] && echo T || echo F`, "T"},
		{`[[ x == @(x|y) ]] && echo T || echo F`, "T"},
		{`[[ '*' == '*' ]] && [[ a != '*' ]] && echo T || echo F`, "T"},
		{`case '@(x)' in '@(x)') echo T;; *) echo F;; esac`, "T"},
		{`case x in '@(x)') echo T;; *) echo F;; esac`, "F"},
		{`case x in @(x|y)) echo T;; *) echo F;; esac`, "T"},
	} {
		var out bytes.Buffer
		if _, err := New().Execute(context.Background(), tc.line, &out, io.Discard); err != nil {
			t.Fatalf("%s: %v", tc.line, err)
		}
		if got := strings.TrimSpace(out.String()); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.line, got, tc.want)
		}
	}
}
