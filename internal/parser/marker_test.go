package parser

import (
	"strings"
	"testing"
)

func TestParse_MarkerRules(t *testing.T) {
	tests := []struct {
		line   string
		typ    CommandType
		cmd    string
		prompt string
	}{
		// Documented syntax keeps working.
		{"?? find large files", CommandTypeAgent, "", "find large files"},
		{"??find", CommandTypeAgent, "", "find"},
		{"ls | ?? filter", CommandTypeAgentPipe, "ls", "filter"},
		{"ls |?? filter", CommandTypeAgentPipe, "ls", "filter"},
		{"cmd1 | cmd2 | ?? prompt", CommandTypeAgentPipe, "cmd1 | cmd2", "prompt"},
		{`echo "a|b" | ?? explain`, CommandTypeAgentPipe, `echo "a|b"`, "explain"},
		{`echo "${x:-"it's"}" | ?? explain`, CommandTypeAgentPipe, `echo "${x:-"it's"}"`, "explain"},
		{`echo "${x#"("}" | ?? explain`, CommandTypeAgentPipe, `echo "${x#"("}"`, "explain"},
		{"git log --format=?? oneline", CommandTypeAgentInline, "git log --format=", "oneline"},
		{"cmd --flag=??", CommandTypeAgentInline, "cmd --flag=", ""},
		{"cmd --flag= ?? value", CommandTypeAgentInline, "cmd --flag=", "value"},
		{"jj log -r @ ?? only the id", CommandTypeAgentInline, "jj log -r @ ", "only the id"},
		{"cmd\t??\tprompt", CommandTypeAgentInline, "cmd\t", "prompt"},
		{`cmd "arg with spaces" ?? prompt`, CommandTypeAgentInline, `cmd "arg with spaces" `, "prompt"},

		// ?? inside quotes, escaped, or in a comment is just text.
		{`echo "really??"`, CommandTypeRegular, `echo "really??"`, ""},
		{`echo "what ?? now"`, CommandTypeRegular, `echo "what ?? now"`, ""},
		{`grep 'a ?? b' file`, CommandTypeRegular, `grep 'a ?? b' file`, ""},
		{`echo "a | ?? b"`, CommandTypeRegular, `echo "a | ?? b"`, ""},
		{`echo \?? x`, CommandTypeRegular, `echo \?? x`, ""},
		{`echo "\"" ?? x`, CommandTypeAgentInline, `echo "\"" `, "x"},
		{`echo hi # ?? note`, CommandTypeRegular, `echo hi # ?? note`, ""},

		// ?? as a glob is a regular command.
		{"ls ??", CommandTypeRegular, "ls ??", ""},
		{"ls ??.go", CommandTypeRegular, "ls ??.go", ""},
		{"ls a??b", CommandTypeRegular, "ls a??b", ""},
		{"ls ???", CommandTypeRegular, "ls ???", ""},

		// A pipe marker wins over an earlier inline one.
		{"cmd ?? x | ?? y", CommandTypeAgentPipe, "cmd ?? x", "y"},

		// || is an OR list, not a pipe.
		{"a || ?? b", CommandTypeAgentInline, "a || ", "b"},
		{"a || b", CommandTypeRegular, "a || b", ""},
	}
	for _, tt := range tests {
		got := Parse(tt.line)
		if got.Type != tt.typ || got.Command != tt.cmd || got.AgentPrompt != tt.prompt {
			t.Errorf("Parse(%q) = {%v %q %q}, want {%v %q %q}",
				tt.line, got.Type, got.Command, got.AgentPrompt, tt.typ, tt.cmd, tt.prompt)
		}
	}
}

// FindAgentMarker is also called on untrimmed text (the completion line up to
// the cursor): leading blanks must not turn a full request into an inline one.
func TestFindAgentMarker_SkipsLeadingBlanks(t *testing.T) {
	for _, line := range []string{"  ?? find files", "\t?? find files", "   ??"} {
		m, ok := FindAgentMarker(line)
		start := len(line) - len(strings.TrimLeft(line, " \t"))
		if !ok || m.Type != CommandTypeAgent || m.Start != start || m.End != start+2 {
			t.Errorf("FindAgentMarker(%q) = %+v, %v; want a full agent marker at %d", line, m, ok, start)
		}
		if got := Parse(line).Type; got != CommandTypeAgent {
			t.Errorf("Parse(%q).Type = %v, want agent", line, got)
		}
	}
	if m, ok := FindAgentMarker("  ls | ?? x"); !ok || m.Type != CommandTypeAgentPipe || m.Start != 5 {
		t.Errorf("FindAgentMarker pipe after blanks = %+v, %v", m, ok)
	}
}

// The inline command is what the fill is appended to, so it must keep the
// boundary the user typed. Only a flag's = (--flag= ??) absorbs the blank
// before ??; after an operator such as == or an empty assignment the blank
// is a word boundary and dropping it glued the fill onto the operator.
// Everything after a bare ?? word is the prompt, also after a command
// separator, where the agent fills the next command.
func TestParse_InlineCommandKeepsTheBoundary(t *testing.T) {
	tests := []struct{ line, cmd, prompt string }{
		{"cmd --flag= ?? value", "cmd --flag=", "value"},
		{"cmd -o= ?? value", "cmd -o=", "value"},
		{"cmd --flag=?? value", "cmd --flag=", "value"},
		{"[[ $x == ?? ]]", "[[ $x == ", "]]"},
		{`[ "$a" != ?? ]`, `[ "$a" != `, "]"},
		{"FOO= ?? run the tests", "FOO= ", "run the tests"},
		{"x=?? a name", "x=", "a name"},
		{"cp ?? /tmp/", "cp ", "/tmp/"},
		{"make; ?? why", "make; ", "why"},
		{"make && ?? deploy it", "make && ", "deploy it"},
	}
	for _, tt := range tests {
		got := Parse(tt.line)
		if got.Type != CommandTypeAgentInline || got.Command != tt.cmd || got.AgentPrompt != tt.prompt {
			t.Errorf("Parse(%q) = {%v %q %q}, want inline %q %q", tt.line, got.Type, got.Command, got.AgentPrompt, tt.cmd, tt.prompt)
		}
	}
}

// The marker search reads quoting the way the shell does: a comment starts
// at a # after any operator, $'...' escapes its quote, quotes nest inside
// $( ), an escaped blank or | is a literal byte, and a ?? inside a
// substitution does not split the line.
func TestParse_MarkerFollowsShellQuoting(t *testing.T) {
	tests := []struct {
		line   string
		typ    CommandType
		cmd    string
		prompt string
	}{
		{"echo foo;# ?? note", CommandTypeRegular, "echo foo;# ?? note", ""},
		{"echo foo|# ?? note", CommandTypeRegular, "echo foo|# ?? note", ""},
		{`echo $'a\'b' ?? q`, CommandTypeAgentInline, `echo $'a\'b' `, "q"},
		{`echo "$(echo "it's")" ?? q`, CommandTypeAgentInline, `echo "$(echo "it's")" `, "q"},
		{"echo `echo \"it's\"` ?? q", CommandTypeAgentInline, "echo `echo \"it's\"` ", "q"},
		{`echo a\ ?? q`, CommandTypeRegular, `echo a\ ?? q`, ""},
		{`echo \|| ?? q`, CommandTypeAgentPipe, `echo \|`, "q"},
		{"echo $(ls | ?? q)", CommandTypeRegular, "echo $(ls | ?? q)", ""},
		{"echo $(ls ?? q)", CommandTypeRegular, "echo $(ls ?? q)", ""},
		{"(( a << b )) ?? q", CommandTypeAgentInline, "(( a << b )) ", "q"},
		{"(( x == ?? ))", CommandTypeRegular, "(( x == ?? ))", ""},
		{`cmd --flag\=?? q`, CommandTypeAgentInline, `cmd --flag\=`, "q"},

		// A comment ends at the newline; a heredoc body is data.
		{"echo # note\nls ?? q", CommandTypeAgentInline, "echo # note\nls ", "q"},
		{"cat <<EOF\na ?? q\nEOF", CommandTypeRegular, "cat <<EOF\na ?? q\nEOF", ""},
	}
	for _, tt := range tests {
		got := Parse(tt.line)
		if got.Type != tt.typ || got.Command != tt.cmd || got.AgentPrompt != tt.prompt {
			t.Errorf("Parse(%q) = {%v %q %q}, want {%v %q %q}",
				tt.line, got.Type, got.Command, got.AgentPrompt, tt.typ, tt.cmd, tt.prompt)
		}
	}
}

// At the start of the line and after | the marker needs no blank after it,
// unlike the inline one: the doc on FindAgentMarker says so.
func TestFindAgentMarker_GluedAtStartAndAfterPipe(t *testing.T) {
	for line, want := range map[string]CommandType{
		"??? why":    CommandTypeAgent,
		"ls | ??.go": CommandTypeAgentPipe,
		"ls|??x":     CommandTypeAgentPipe,
		"ls ??.go":   CommandTypeRegular,
	} {
		m, ok := FindAgentMarker(line)
		got := CommandTypeRegular
		if ok {
			got = m.Type
		}
		if got != want {
			t.Errorf("%q: type %v, want %v", line, got, want)
		}
	}
}
