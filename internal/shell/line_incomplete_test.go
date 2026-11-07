package shell

import (
	"testing"

	"github.com/tfcace/hash/internal/config"
)

// The shell's editors read a line as unfinished (Enter adds a row, as at
// bash's PS2) when the executor's dialect would: an open compound, heredoc,
// quote or pipe. An agent prompt is never unfinished: its quotes are prose.
// The conversation's reply box is no shell line at all.
func TestEditorIncomplete_ShellLinesInTheExecutorsDialect(t *testing.T) {
	for _, dialect := range []string{"bash", "zsh"} {
		cfg := config.Default()
		cfg.Shell.Dialect = dialect
		sh, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		incomplete := sh.editorCfg.Incomplete
		if incomplete == nil {
			t.Fatalf("%s: the shell's editor has no Incomplete hook", dialect)
		}
		for text, want := range map[string]bool{
			"for i in 1 2; do":                true,
			"for i in 1 2; do\necho $i":       true,
			"for i in 1 2; do\ndone":          false,
			"cat <<EOF":                       true,
			"cat <<EOF\nhi\nEOF":              false,
			`printf '[%s]' "a`:                true,
			"ls |":                            true,
			`ls \`:                            true,
			"echo hi":                         false,
			"echo )":                          false, // an error, not unfinished: it runs and says so
			"":                                false,
			`?? what's "this`:                 false,
			`git log | ?? what's "this`:       false,
			"echo # a comment's backslash \\": false,
		} {
			if got := incomplete(text); got != want {
				t.Errorf("%s: Incomplete(%q) = %v, want %v", dialect, text, got, want)
			}
		}
		if reply := agentConversationEditorConfig(sh.editorCfg, false); reply.Incomplete != nil {
			t.Errorf("%s: the conversation's reply box reads its text as shell", dialect)
		}
		if edit := sh.agentTextEditorConfig(); edit.Incomplete == nil {
			t.Errorf("%s: the editor for an agent's command does not read it as shell", dialect)
		}
	}
}

// mvdan decides whether a line is unfinished; the lexer is only asked
// whether the line ends with an escaped newline. Its own reading of (# as a
// group and a comment never keeps a line bash runs from running.
func TestLineIncomplete_ParsedLinesRun(t *testing.T) {
	cfg := config.Default()
	cfg.Shell.Dialect = "bash"
	sh, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]bool{
		`[[ $l =~ ^(#|//) ]] && echo m`:   false,
		`ls !(#*#)`:                       false,
		`ls @(#a|b)`:                      false,
		`[[ $x == +(#) ]]`:                false,
		`ls \`:                            true,
		"echo a \\\n":                     false,
		"echo # a comment's backslash \\": false,
	} {
		if got := sh.lineIncomplete(text); got != want {
			t.Errorf("lineIncomplete(%q) = %v, want %v", text, got, want)
		}
	}
}

// A here-document reads on until its delimiter line whether the delimiter
// is quoted or not: mvdan marks only the unquoted one's missing end line as
// unfinished, so Enter on `cat <<'EOF'` ran the line and each body line
// after it ran as a command.
func TestLineIncomplete_QuotedHeredocReadsOn(t *testing.T) {
	for _, dialect := range []string{"bash", "zsh"} {
		cfg := config.Default()
		cfg.Shell.Dialect = dialect
		sh, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for text, want := range map[string]bool{
			"cat <<'EOF'":                 true,
			"cat <<'EOF'\nfoo":            true,
			`cat <<"EOF"`:                 true,
			"cat <<\"EOF\"\nfoo":          true,
			`cat <<\EOF`:                  true,
			"cat <<\\EOF\nfoo":            true,
			"cat <<-'EOF'":                true,
			"cat <<-'EOF'\n\tfoo":         true,
			"x=$(cat <<'EOF'\nfoo":        true,
			"cat <<'EOF'\nfoo\nEOF":       false,
			"cat <<\"EOF\"\nfoo\nEOF":     false,
			"cat <<\\EOF\nfoo\nEOF":       false,
			"cat <<-'EOF'\n\tfoo\n\tEOF":  false,
			"cat <<'EOF' | wc -l\na\nEOF": false,
		} {
			if got := sh.lineIncomplete(text); got != want {
				t.Errorf("%s: lineIncomplete(%q) = %v, want %v", dialect, text, got, want)
			}
		}
		sh.Close()
	}
}
