package completion

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/shlex"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// literalVCS lists the given values as git refs and modified files, as a
// fetched branch or a file in the work tree can be named.
func literalVCS(values ...string) *VCSCompleter {
	c := NewVCSCompleter()
	list := func(context.Context, string) ([]string, error) { return values, nil }
	c.listGitRefs, c.listGitModified = list, list
	c.policy.wait = 5 * time.Second // the lister answers at once; a loaded machine must not make it pending
	return c
}

func literalRouter(values ...string) *Router {
	router := NewRouter()
	router.Register(literalVCS(values...), PriorityVCS)
	return router
}

// A completer's values are what the word should mean; what Tab inserts is
// shell text that means that, wherever it goes: escaped in an unquoted word,
// inside the quote the user opened (closed after a complete value), so a
// branch named "origin/x;id" never runs id.
func TestRouter_InsertsValuesAsShellText(t *testing.T) {
	tests := []struct{ line, value, want string }{
		{"git add e", "e f.txt", `git add e\ f.txt`},
		{"git add <(ls) e", "e f.txt", `git add <(ls) e\ f.txt`},
		{"git checkout or", "origin/x;id", `git checkout origin/x\;id`},
		{"git checkout or", "origin/$(id)`id`", "git checkout origin/\\$\\(id\\)\\`id\\`"},
		{`git add "e`, "e f.txt", `git add "e f.txt"`},
		{`git add "e`, `e"$x.txt`, `git add "e\"\$x.txt"`},
		{`git add 'e`, "e f.txt", `git add 'e f.txt'`},
		{`git add 'e`, "e'f.txt", `git add 'e'\''f.txt'`},
		{`git add $'e`, `e'\f`, `git add $'e\'\\f'`},
		{`git add "sub/`, "sub/dir/", `git add "sub/dir/`},
		{`echo "$(git add e`, "e f.txt", `echo "$(git add e\ f.txt`},
		{"git add ~", "~root", `git add \~root`},
	}
	for _, tt := range tests {
		result, _ := literalRouter(tt.value).Complete(context.Background(), tt.line, len(tt.line))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v, want %q", tt.line, result.Items, tt.value)
			continue
		}
		if got := applied(tt.line, result); got != tt.want {
			t.Errorf("Complete(%q) of %q applied = %s, want %s", tt.line, tt.value, got, tt.want)
		}
	}
}

// Whatever a value holds, the word Tab writes reads back as that value.
func TestRouter_InsertedWordReadsBackAsTheValue(t *testing.T) {
	values := []string{
		"a b", "a;id", "$(id)", "`id`", "a'b", `a"b`, `a\b`, "*", "#x", "!x", "a|b&c",
		"x>y", "{a,b}", "a\tb", "ünï cödé", "$HOME", "a\nb", "a\r\nb", "a\rb", "\x1b[0m",
	}
	for _, open := range []string{"", `"`, "'", "$'"} {
		for _, value := range values {
			line := "git add " + open
			result, _ := literalRouter(value).Complete(context.Background(), line, len(line))
			if len(result.Items) != 1 {
				t.Errorf("Complete(%q) of %q = %#v", line, value, result.Items)
				continue
			}
			got := applied(line, result)
			if word, err := lastWord(got); err != nil || word != value {
				t.Errorf("Complete(%q) of %q applied %q, which reads back as %q (%v)", line, value, got, word, err)
			}
		}
	}
}

// lastWord parses line as shell and returns its last word, expanded as an
// argument: one field after quote removal.
func lastWord(line string) (string, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(line), "")
	if err != nil {
		return "", err
	}
	call, ok := file.Stmts[len(file.Stmts)-1].Cmd.(*syntax.CallExpr)
	if !ok {
		return "", fmt.Errorf("not a simple command")
	}
	fields, err := expand.Fields(nil, call.Args[len(call.Args)-1])
	if err != nil || len(fields) != 1 {
		return strings.Join(fields, " | "), fmt.Errorf("%d fields, %v", len(fields), err)
	}
	return fields[0], nil
}

// A quoted heredoc body is text no shell reads: a name goes in as it is.
// The body of an unquoted heredoc expands $ and `, so they (and \) are
// escaped there, and nothing else. A comment is escaped as code: no shell
// reads it, and where the lexer takes code for one the name still means
// itself.
func TestRouter_InsertsTextWordsAsTheyAre(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"it's (1).txt", "a$x`id`.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := NewFileCompleter()
	files.getwd = func() string { return dir }
	router := NewRouter()
	router.Register(files, PriorityFilesystem)
	tests := []struct{ line, want string }{
		{"echo # see it", `echo # see it\'s\ \(1\).txt`},
		{"cat <<'EOF'\nsee it", "cat <<'EOF'\nsee it's (1).txt"},
		{"cat <<EOF\nsee it", "cat <<EOF\nsee it's (1).txt"},
		{"echo # a$", "echo # a\\$x\\`id\\`.txt"},
		{"cat <<'EOF'\na$", "cat <<'EOF'\na$x`id`.txt"},
		{"cat <<EOF\na$", "cat <<EOF\na\\$x\\`id\\`.txt"},
	}
	for _, tt := range tests {
		result, _ := router.Complete(context.Background(), tt.line, len(tt.line))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v, want one name", tt.line, result.Items)
			continue
		}
		if got := applied(tt.line, result); got != tt.want {
			t.Errorf("Complete(%q) applied %q, want %q", tt.line, got, tt.want)
		}
	}
}

// A $( ), backticks or ${ } in the body of an unquoted heredoc hold code
// again: a word there is escaped like any argument, so a file named
// "a;touch PWNED" does not run touch when the heredoc is read.
func TestRouter_EscapesCodeInsideAnExpandingBody(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a;touch PWNED"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files := NewFileCompleter()
	files.getwd = func() string { return dir }
	router := NewRouter()
	router.Register(files, PriorityFilesystem)
	tests := []struct{ line, want string }{
		{"cat <<EOF\n$(ls a", `cat <<EOF` + "\n" + `$(ls a\;touch\ PWNED`},
		{"cat <<EOF\nx `ls a", "cat <<EOF\nx `ls a\\\\;touch\\\\ PWNED"}, // once more in backticks
		{"cat <<EOF\n$(ls\na", "cat <<EOF\n$(ls\na\\;touch\\ PWNED"},
		{"cat <<'EOF'\n$(ls a", "cat <<'EOF'\n$(ls a;touch PWNED"},
	}
	for _, tt := range tests {
		result, _ := router.Complete(context.Background(), tt.line, len(tt.line))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v, want one name", tt.line, result.Items)
			continue
		}
		if got := applied(tt.line, result); got != tt.want {
			t.Errorf("Complete(%q) applied %q, want %q", tt.line, got, tt.want)
		}
	}
}

// The shell reads the text of backticks once more after taking a
// backslash off \\, \` and \$, so a word inside them is escaped once more:
// a file named "`touch$IFS.PWNED2`x" does not run touch.
func TestRouter_EscapesOnceMoreInsideBackticks(t *testing.T) {
	tests := []struct{ line, value, want string }{
		{"echo `git add ", "`touch$IFS.PWNED2`x", "echo `git add \\\\\\`touch\\\\\\$IFS.PWNED2\\\\\\`x"},
		{"echo `git add ", `a\b`, "echo `git add a\\\\\\\\b"},
		{"echo \"`git add \"", "$HOME", "echo \"`git add \"\\\\\\$HOME\""},
	}
	for _, tt := range tests {
		result, _ := literalRouter(tt.value).Complete(context.Background(), tt.line, len(tt.line))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v, want %q", tt.line, result.Items, tt.value)
			continue
		}
		if got := applied(tt.line, result); got != tt.want {
			t.Errorf("Complete(%q) of %q applied %s, want %s", tt.line, tt.value, got, tt.want)
		}
	}
}

// hash's parser keeps the backslash of an escape in a redirection target
// or a variable's value (echo hi > a\ b creates "a\ b"), so a value goes
// there in quotes; run by hash, the line writes the file the value names
// and sets the variable to it.
func TestRouter_QuotesWhereHashKeepsBackslashes(t *testing.T) {
	tests := []struct{ before, value, want, after string }{
		{"echo hi > ", "a b", "echo hi > 'a b'", ""},
		{"echo hi >", "it's", "echo hi >$'it\\'s'", ""},
		{"x=", "a;b", "x='a;b'", "; printf %s \"$x\" > out"},
		{"export x=", "plain", "export x=plain", "; printf %s \"$x\" > out"},
	}
	for _, tt := range tests {
		router := NewRouter()
		router.Register(oneValue{value: tt.value}, PriorityPlugin)
		result, _ := router.Complete(context.Background(), tt.before, len(tt.before))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v", tt.before, result.Items)
			continue
		}
		got := applied(tt.before, result)
		if got != tt.want {
			t.Errorf("Complete(%q) of %q applied %s, want %s", tt.before, tt.value, got, tt.want)
		}
		dir := t.TempDir()
		// The executor runs in this process: it changes its directory and
		// environment, which the cleanups put back.
		t.Chdir(dir)
		for _, name := range []string{"x", "HASH_SHELL", "SHELL"} {
			t.Setenv(name, os.Getenv(name))
		}
		var out, errs bytes.Buffer
		if _, err := executor.New().Execute(context.Background(), got+tt.after, &out, &errs); err != nil {
			t.Fatal(err)
		}
		name, content := tt.value, "hi\n"
		if tt.after != "" {
			name, content = "out", tt.value
		}
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != content {
			entries, _ := os.ReadDir(dir)
			t.Errorf("hash ran %q: %s = %q (%v), want %q; dir %v", got+tt.after, name, b, err, content, entries)
		}
	}
}

// A ~ the user typed expands only outside quotes, and the file completer
// lists the home directory for it: in a quote the user opened before it,
// the ~/ stays outside the quote, so the path still names the home
// directory.
func TestRouter_KeepsATypedTildeOutsideTheQuote(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "My Docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRouter()
	router.Register(NewFileCompleter(), PriorityFilesystem)
	tests := []struct{ line, want string }{
		{"cd ~/My", `cd ~/My\ Docs/`},
		{`cd "~/My`, `cd ~/"My Docs/`},
		{`cat '~/no`, `cat ~/'notes.txt'`},
	}
	for _, tt := range tests {
		result, _ := router.Complete(context.Background(), tt.line, len(tt.line))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v, want one item", tt.line, result.Items)
			continue
		}
		if got := applied(tt.line, result); got != tt.want {
			t.Errorf("Complete(%q) applied %s, want %s", tt.line, got, tt.want)
		}
	}
}

// Completing inside a quote the line already closes after the cursor
// writes no closing quote of its own, which would leave the later one open.
func TestRouter_LeavesALaterClosingQuoteToTheLine(t *testing.T) {
	tests := []struct{ line, rest, want string }{
		{`git add "e`, `" | wc`, `git add "e f.txt" | wc`},
		{`git add 'e`, `' x`, `git add 'e f.txt' x`},
		{`git add "e`, ` x`, `git add "e f.txt" x`},
	}
	for _, tt := range tests {
		line := tt.line + tt.rest
		result, _ := literalRouter("e f.txt").Complete(context.Background(), line, len(tt.line))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v", line, result.Items)
			continue
		}
		if got := applied(tt.line, result) + tt.rest; got != tt.want {
			t.Errorf("Complete(%q at %d) leaves %s, want %s", line, len(tt.line), got, tt.want)
		}
	}
}

// A heredoc body has no way to write a newline that stays in it: one would
// end the body line. Such a name is not offered there. A comment takes it
// escaped as code ($'\n'), so it does not end the comment either.
func TestRouter_OffersNoNewlineInText(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a\nid", "ab"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := NewFileCompleter()
	files.getwd = func() string { return dir }
	router := NewRouter()
	router.Register(files, PriorityFilesystem)
	for _, line := range []string{"cat <<EOF\na", "cat <<'EOF'\na"} {
		result, _ := router.Complete(context.Background(), line, len(line))
		if len(result.Items) != 1 || result.Items[0].Value != "ab" {
			t.Errorf("Complete(%q) = %#v, want ab alone", line, result.Items)
		}
	}
}

// Only ~/ and ~name/, with a name a user can have, expand: ~'x;id'/ is
// quoted, so it names the directory "~x;id" here, and the name is
// escaped like any other text, not written back as ~x;id/ to run id.
// ~name/ is name's home, not a directory in $HOME, and stays as typed.
func TestRouter_KeepsOnlyARealTildePrefix(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	for _, path := range []string{"x;id/f", "no_such_hash_user/g", "todo.txt"} {
		writeTestFile(t, filepath.Join(home, path))
	}
	writeTestFile(t, filepath.Join(cwd, "~x;id", "h"))
	files := NewFileCompleter()
	files.getwd = func() string { return cwd }
	router := NewRouter()
	router.Register(files, PriorityFilesystem)
	tests := []struct{ line, want string }{
		{"ls ~'x;id'/", `ls \~x\;id/h`},
		{`ls ~x\;id/`, `ls \~x\;id/h`},
		{"ls ~/to", `ls ~/todo.txt`},
		{"ls ~no_such_hash_user/", ""},
	}
	for _, tt := range tests {
		result, _ := router.Complete(context.Background(), tt.line, len(tt.line))
		got := ""
		if len(result.Items) > 0 {
			got = applied(tt.line, result)
		}
		if got != tt.want || len(result.Items) > 1 {
			t.Errorf("Complete(%q) = %d items, applied %s, want %s", tt.line, len(result.Items), got, tt.want)
		}
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A replacement that starts right after a backslash the user typed goes on
// with that escape: the first byte is the escaped one, and the rest is
// escaped as anywhere else, so "$x$(id)" after it runs nothing. Where the
// backslash is more than an escape of the next byte nothing is offered.
func TestRouter_ContinuesATypedEscape(t *testing.T) {
	tests := []struct{ line, value, want string }{
		{`probe a\`, "$HOME", `probe a\$HOME`},
		{"cat <<EOF\nz\\", "$x$(id)", "cat <<EOF\nz\\$x\\$(id)"},
		{`probe "a\`, `"$x`, `probe "a\"\$x`},
		{`probe $'a\`, "n", ""},
		{"echo `probe a\\", "x", ""},
		{`probe > a\`, "b", ""},
		{`probe a\`, "\nx", ""},
	}
	for _, tt := range tests {
		router := NewRouter()
		router.Register(oneValue{value: tt.value}, PriorityPlugin)
		result, _ := router.Complete(context.Background(), tt.line, len(tt.line))
		got := ""
		if len(result.Items) == 1 {
			got = applied(tt.line, result)
		}
		if got != tt.want {
			t.Errorf("Complete(%q) of %q applied %q, want %q", tt.line, tt.value, got, tt.want)
		}
	}
}

// The reported injections, end to end: names a file completion offers in
// a $( ) of a heredoc body and inside backticks, accepted and run by hash
// in a directory that holds them, run no touch.
func TestRouter_CompletedNamesRunNothingInHash(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a;touch PWNED1", "`touch$IFS.PWNED2`x", "b$(touch PWNED3)"} {
		writeTestFile(t, filepath.Join(dir, name))
	}
	files := NewFileCompleter()
	files.getwd = func() string { return dir }
	router := NewRouter()
	router.Register(files, PriorityFilesystem)
	t.Chdir(dir) // the executor runs in this process and cds there
	for _, name := range []string{"HASH_SHELL", "SHELL"} {
		t.Setenv(name, os.Getenv(name))
	}
	tests := []struct{ before, after string }{
		{"cat <<EOF\n$(ls a", ")\nEOF\n"},
		{"echo `ls ", "`"},
		{"cat <<EOF\n`ls b", "`\nEOF\n"},
		{"echo \"`ls ", "`\""},
	}
	ran := 0
	for _, tt := range tests {
		line := tt.before + tt.after
		result, _ := router.Complete(context.Background(), line, len(tt.before))
		for _, item := range result.Items {
			var out, errs bytes.Buffer
			src := line[:result.ReplaceFrom] + item.Value + tt.after
			if _, err := executor.New().Execute(context.Background(), src, &out, &errs); err != nil {
				t.Errorf("%q: %v", src, err)
			}
			ran++
		}
	}
	if ran < len(tests) {
		t.Errorf("ran %d completed lines, want at least %d", ran, len(tests))
	}
	for _, name := range []string{"PWNED1", ".PWNED2", "PWNED3"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("a completed name ran touch %s", name)
		}
	}
}

// A name that replaces text the lexer reads as a comment ("#x", the start
// of a comment glued to nothing) is written as code: the replacement turns
// the comment into the argument, so a file named "a;id #x" found by fuzzy
// matching "#x" must not run id.
func TestRouter_WritesACommentAsCode(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a;id #x"))
	files := NewFileCompleter()
	files.getwd = func() string { return dir }
	files.SetFuzzyMode(true)
	router := NewRouter()
	router.SetFuzzy(true)
	router.Register(files, PriorityFilesystem)
	for _, line := range []string{"probe #x", "probe q # x"} {
		result, _ := router.Complete(context.Background(), line, len(line))
		if len(result.Items) != 1 {
			t.Fatalf("Complete(%q) = %#v, want the file", line, result.Items)
		}
		got := applied(line, result)
		file, err := syntax.NewParser().Parse(strings.NewReader(got), "")
		if err != nil || len(file.Stmts) != 1 {
			t.Errorf("Complete(%q) applied %q: %d commands, %v", line, got, len(file.Stmts), err)
		}
	}
	line := "probe #x"
	result, _ := router.Complete(context.Background(), line, len(line))
	if got, _ := lastWord(applied(line, result)); got != "a;id #x" {
		t.Errorf("Complete(%q) applied %q, which reads back as %q", line, applied(line, result), got)
	}
}

// A directory's opening quote stays open only at the end of the line, for
// the user to go on into the directory: with text after the cursor it
// would swallow that text (a closing backtick, the next word).
func TestRouter_LeavesADirectorysQuoteOpenOnlyAtTheEnd(t *testing.T) {
	tests := []struct{ before, after, want string }{
		{`git add "su`, "", `git add "sub/`},
		{"echo `git add \"su", "`", "echo `git add \"sub/\"`"},
		{`git add "su`, " x", `git add "sub/" x`},
	}
	for _, tt := range tests {
		line := tt.before + tt.after
		result, _ := literalRouter("sub/").Complete(context.Background(), line, len(tt.before))
		if len(result.Items) != 1 {
			t.Errorf("Complete(%q) = %#v, want sub/", line, result.Items)
			continue
		}
		if got := line[:result.ReplaceFrom] + result.Items[0].Value + tt.after; got != tt.want {
			t.Errorf("Complete(%q) applied %q, want %q", line, got, tt.want)
		}
	}
}

// A command that parses its argument again as code (eval, alias, trap, a
// shell's -c script) would read an escaped value's text as code: only a
// plain word is offered there, which means itself both times.
func TestRouter_OffersOnlyPlainWordsWhereTheValueIsReadAgain(t *testing.T) {
	complete := func(before, after, value string) []Item {
		router := NewRouter()
		router.Register(oneValue{value, false}, PriorityPlugin)
		got, _ := router.Complete(context.Background(), before+after, len(before))
		return got.Items
	}
	for _, tt := range []struct{ before, after string }{
		{"eval probe ", ""},
		{`alias zz="probe `, `"`},
		{`trap "probe `, `" EXIT`},
		{`sh -c "probe `, `"`},
		{"bash -lc 'probe ", "'"},
		{`zsh -c -- "probe `, `"`},
		{`sudo sh -c "probe `, `"`},
		{"command eval probe ", ""},
		{`env X=1 bash -c "probe `, `"`},
		{`eval "$(echo probe `, `)"`},
		{`hash -c "probe `, `"`},
		{`/usr/local/bin/hash -c "probe `, `"`},
		{`\eval probe `, ""},
		{`'eval' probe `, ""},
		{`env -u NAME sh -c "probe `, `"`},
		{`exec -a n sh -c "probe `, `"`},
		{`command -p eval probe `, ""},
		{`sudo -u root sh -c "probe `, `"`},
		{`nohup bash -c "probe `, `"`},
		// an option cluster's argument may be attached (-uroot), and a
		// long option's may be the next word (--user root)
		{`sudo -uroot sh -c "probe `, `"`},
		{`sudo -ugit bash -c "probe `, `"`},
		{`env -uFOOS sh -c "probe `, `"`},
		{`sudo --user root sh -c "probe `, `"`},
		{`sudo --user=root sh -c "probe `, `"`},
		{`sudo -- sh -c "probe `, `"`},
		{`env -i -C /tmp sh -c "probe `, `"`},
		// runners with operands before the command, and commands that take
		// a shell command as an option's argument or as their arguments
		{`timeout 5 sh -c "probe `, `"`},
		{`timeout -s KILL 5s bash -c "probe `, `"`},
		{`timeout --signal=KILL -k 1 5 sh -c "probe `, `"`},
		{`nice sh -c "probe `, `"`},
		{`nice -n 10 sh -c "probe `, `"`},
		{`nice -10 sh -c "probe `, `"`},
		{`flock /tmp/l sh -c "probe `, `"`},
		{`flock -w 5 /tmp/l -c "probe `, `"`},
		{`flock -c "probe `, `" /tmp/l`},
		{`su -c "probe `, `"`},
		{`su root -c "probe `, `"`},
		{`script -q -c "probe `, `" /dev/null`},
		{`watch probe `, ""},
		{`watch -n 1 ls probe `, ""},
		{`sudo timeout 5 nice -n 1 sh -c "probe `, `"`},
		// a command attached to its option
		{`flock /tmp/l -c"probe `, `"`},
		{`flock /tmp/l --command="probe `, `"`},
		{`su -c"probe `, `"`},
		{`su --command="probe `, `"`},
		{`script -qc"probe `, `" /dev/null`},
		{`fish --command="probe `, `"`},
		{`fish -c"probe `, `"`},
		{`fish -ic"probe `, `"`},
		{`fish -c true -c"probe `, `"`},
		{`sudo -iu root fish --command="probe `, `"`},
		{`stdbuf -oL fish -c"probe `, `"`},
		// a long option's command in the next word, and the options before
		// the command that take an argument
		{`su --command "probe `, `"`},
		{`su root --command "probe `, `"`},
		{`su --session-command "probe `, `"`},
		{`script --command "probe `, `" /dev/null`},
		{`script -q -E never --command "probe `, `" /dev/null`},
		{`sudo -R /tmp sh -c "probe `, `"`},
		{`sudo -R /tmp eval probe `, ""},
		// runners it does not know: a shell's -c script, or su's, after
		// any of them is code, and so is what env -S splits
		{`chroot /x sh -c "probe `, `"`},
		{`setsid sh -c "probe `, `"`},
		{`stdbuf -oL sh -c "probe `, `"`},
		{`caffeinate -i sh -c "probe `, `"`},
		{`xargs -I{} sh -c "probe `, `"`},
		{`ionice -c 3 sh -c "probe `, `"`},
		{`runuser -u x -- sh -c "probe `, `"`},
		{`sudo -- -u sh -c "probe `, `"`},
		{`stdbuf -oL fish --command "probe `, `"`},
		{`stdbuf -oL su -c "probe `, `"`},
		{`env -S "sh -c" "probe `, `"`},
		{`env -S"sh -c" "probe `, `"`},
		{`env --split-string "probe `, `"`},
	} {
		if got := complete(tt.before, tt.after, "a;pwn"); len(got) != 0 {
			t.Errorf("%q + a;pwn: offered %+v, want nothing", tt.before, got)
		}
		if got := complete(tt.before, tt.after, "src/main.go"); len(got) != 1 || got[0].Value != "src/main.go" {
			t.Errorf("%q + src/main.go: offered %+v", tt.before, got)
		}
	}
	// a shell's arguments after its script are not code
	for _, before := range []string{
		"bash -c 'echo $1' x ", "sh probe ", "echo eval ",
		"timeout 5 cat ", "nice -n 1 ls ", "sudo -uroot cat ", "flock /tmp/l cat ", "env -uFOO cat ",
		"flock /tmp/l cat -c", "su root ls ",
		"su -s /bin/sh -c id ", "su --command id ", "script -E never ", "sudo -R /tmp cat ",
		"xargs cat ", "chroot /x sh probe ", "echo bash -c 'echo $1' x ",
	} {
		if got := complete(before, "", "a b"); len(got) != 1 || got[0].Value != `a\ b` {
			t.Errorf("%q + a b: offered %+v, want a\\ b", before, got)
		}
	}
}

// fromValue completes with one value replacing from a fixed offset.
type fromValue struct {
	from  int
	value string
}

func (fromValue) Name() string { return "from" }

func (c fromValue) Complete(context.Context, string, int) (Result, error) {
	return Result{Items: []Item{{Value: c.value}}, ReplaceFrom: c.from}, nil
}

// After a typed escape the value replaces a quote the replaced text
// opened: the rest of the value goes outside quotes, as the escape is.
func TestRouter_WritesTheRestOfAnEscapedValueWhereTheEscapeIs(t *testing.T) {
	line := `probe \a"b`
	router := NewRouter()
	router.Register(fromValue{from: len(`probe \`), value: "ab c"}, PriorityPlugin)
	result, _ := router.Complete(context.Background(), line, len(line))
	if len(result.Items) != 1 || applied(line, result) != `probe \ab\ c` {
		t.Errorf("Complete(%q) = %+v, want probe \\ab\\ c", line, result.Items)
	}
}

// A menu whose every item the read-back check refused is not "no
// matches": there were matches, withheld because none could be written
// safely there.
func TestRouter_SaysAMenuWasWithheldWhenNoItemCouldBeWritten(t *testing.T) {
	router := NewRouter()
	router.Register(values{"a;pwn", "b c"}, PriorityPlugin)
	got, err := router.Complete(context.Background(), "eval probe ", len("eval probe "))
	if err != nil || len(got.Items) != 0 || got.Outcome().String() != "withheld" {
		t.Errorf("Complete = %+v, %v, outcome %s, want withheld", got.Items, err, got.Outcome())
	}
	got, _ = router.Complete(context.Background(), "probe ", len("probe "))
	if len(got.Items) != 2 {
		t.Errorf("Complete(probe ) = %+v, want both", got.Items)
	}
}

// A quote the replaced text opens is written with the value, and closed by
// it unless the line closes it, or nothing follows and the value is a
// directory to go on into. After a typed escape the opened quote is gone.
func TestCodePlace_WritesTheQuoteTheReplacedTextOpens(t *testing.T) {
	for _, tt := range []struct {
		line, value, want string
	}{
		{`cat "My`, "My Docs/", `"My Docs/`},
		{`cat "My`, "My File", `"My File"`},
		{`cat 'My`, "My File", `'My File'`},
		{`cat $'My`, "My File", `$'My File'`},
	} {
		in := lexedInsertion(tt.line, len("cat "), len(tt.line), "")
		code, ok := in.place.(codePlace)
		if !ok || code.opens.kind == shlex.NoQuote || code.opens.end != closedAtEnd {
			t.Fatalf("%q: place %+v, want an opened quote at the end", tt.line, in.place)
		}
		if got, _ := in.write(tt.value); got != tt.want {
			t.Errorf("%q + %q = %q, want %q", tt.line, tt.value, got, tt.want)
		}
	}
	for line, want := range map[string]quoteEnd{`cat "My" x`: closedByLine, `cat "My x`: closedByValue} {
		pos := strings.Index(line, "My") + 2
		in := lexedInsertion(line, len("cat "), pos, "")
		code := in.place.(codePlace)
		if code.opens.end != want {
			t.Errorf("%q: opened quote ends %v, want %v", line, code.opens.end, want)
		}
		if after := code.after("x").(codePlace); after.opens != (openedQuote{}) || after.quote != shlex.NoQuote {
			t.Errorf("%q: after an escape %+v, want outside quotes", line, after)
		}
	}
}
