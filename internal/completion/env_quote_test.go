package completion

import (
	"context"
	"testing"
)

func homeEnv() *EnvCompleter {
	c := NewEnvCompleter(nil)
	c.envFunc = func() []string { return []string{"HOME=/home/user"} }
	return c
}

// A $ the shell would not expand (single-quoted or escaped) is literal text,
// not a variable reference.
func TestEnvCompleter_IgnoresUnexpandedDollar(t *testing.T) {
	for _, line := range []string{`echo '$HO`, `echo \$HO`, `echo 'a $HO`, `echo $'a $HO`, `echo "$(echo '$HO`, `echo hi # $HO`} {
		result, _ := homeEnv().Complete(context.Background(), line, len(line))
		if len(result.Items) != 0 {
			t.Errorf("Complete(%q) = %#v, want nothing for a literal $", line, result.Items)
		}
	}
}

// The completer replaces the reference from its $ on, so text in the word
// before the $ is left alone.
func TestEnvCompleter_ReplacesFromTheDollar(t *testing.T) {
	tests := []struct{ line, want string }{
		{"echo $HO", "echo $HOME"},
		{`echo "$HO`, `echo "$HOME`},
		{"export PATH=$HO", "export PATH=$HOME"},
		{"cd $HOME/x:$HO", "cd $HOME/x:$HOME"},
		{`echo "$(echo $HO`, `echo "$(echo $HOME`},
		{"echo ${HO", "echo ${HOME"},
		{`echo "${HO`, `echo "${HOME`},
	}
	for _, tt := range tests {
		result, _ := homeEnv().Complete(context.Background(), tt.line, len(tt.line))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v, want one item", tt.line, result.Items)
			continue
		}
		if got := applied(tt.line, result); got != tt.want {
			t.Errorf("Complete(%q) applied = %q, want %q", tt.line, got, tt.want)
		}
	}
}

// In fuzzy mode the router filters on the text the items replace; the head
// before the $ is not part of any variable name.
func TestRouter_FuzzyFiltersEnvAfterWordHead(t *testing.T) {
	router := NewRouter()
	router.SetFuzzy(true)
	router.Register(homeEnv(), PriorityEnv)
	line := "export PATH=$HO"
	result, _ := router.Complete(context.Background(), line, len(line))
	if len(result.Items) != 1 || applied(line, result) != "export PATH=$HOME" {
		t.Fatalf("result = %#v (from %d), want export PATH=$HOME", result.Items, result.ReplaceFrom)
	}
}

// applied is line with result's first item accepted: Prefix+Value replace
// the text from ReplaceFrom to the cursor at the end of line.
func applied(line string, result Result) string {
	return line[:result.ReplaceFrom] + result.Prefix + result.Items[0].Value
}

// The replacement starts where the completed text does, which may be on an
// earlier row or before blanks the editor would split at: text before the
// $ is kept exactly once.
func TestRouter_EnvReplacementKeepsTheTextBeforeIt(t *testing.T) {
	router := NewRouter()
	router.Register(homeEnv(), PriorityEnv)
	tests := []struct{ line, want string }{
		{"echo $HO", "echo $HOME"},
		{"echo $((1 + $HO", "echo $((1 + $HOME"},
		{"echo $(( $HO", "echo $(( $HOME"},
		{"echo \"foo $HO", "echo \"foo $HOME"},
		{"echo \"foo\nbar $HO", "echo \"foo\nbar $HOME"},
		{"echo \\\n$HO", "echo \\\n$HOME"},
		{"echo ${x:-a $HO", "echo ${x:-a $HOME"},
		{"export PATH=$HO", "export PATH=$HOME"},
		{"true;echo x$HO", "true;echo x$HOME"},
		{"true;$HO", "true;$HOME"},
	}
	for _, tt := range tests {
		result, _ := router.Complete(context.Background(), tt.line, len(tt.line))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v, want one item", tt.line, result.Items)
			continue
		}
		if got := applied(tt.line, result); got != tt.want {
			t.Errorf("Complete(%q) applied = %q, want %q", tt.line, got, tt.want)
		}
	}
}

// A closing brace already typed after the cursor stays a brace of the same
// expansion: Tab on "${HO|}x" gives "${HOME}x", not "$HOME}x".
func TestEnvCompleter_KeepsTheBracesOfAnExpansion(t *testing.T) {
	line := "echo ${HO}x"
	pos := len("echo ${HO")
	result, _ := homeEnv().Complete(context.Background(), line, pos)
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v, want HOME", result.Items)
	}
	if got := line[:result.ReplaceFrom] + result.Prefix + result.Items[0].Value + line[pos:]; got != "echo ${HOME}x" {
		t.Fatalf("applied = %q, want %q", got, "echo ${HOME}x")
	}
}

// The body of a heredoc whose delimiter is unquoted expands $VAR like double
// quotes do; a quoted delimiter's body, or an escaped $, is literal.
func TestEnvCompleter_CompletesInAnUnquotedHeredocBody(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"cat <<EOF\n$HO", true},
		{"cat <<EOF\nhello $HO", true},
		{"cat <<-EOF\n\t$HO", true},
		{"cat <<EOF\na\\\n$HO", true},
		{"cat <<'EOF'\n$HO", false},
		{"cat <<\"EOF\"\n$HO", false},
		{"cat <<\\EOF\n$HO", false},
		{"cat <<EOF\n\\$HO", false},
		{"cat <<EOF\nx\nEOF\necho $HO", true},
	}
	for _, tt := range tests {
		result, _ := homeEnv().Complete(context.Background(), tt.line, len(tt.line))
		if got := len(result.Items) == 1; got != tt.want {
			t.Errorf("Complete(%q) = %#v, want an item: %v", tt.line, result.Items, tt.want)
		}
	}
}
