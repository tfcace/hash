package completion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func refsVCS() *VCSCompleter {
	c := NewVCSCompleter()
	c.listGitRefs = func(ctx context.Context, dir string) ([]string, error) {
		return []string{"main", "feature/api"}, nil
	}
	return c
}

// Completers must see the same words the shell would: the command under the
// cursor after ;, &&, ||, |, $( and backticks, with quotes and escapes
// removed.
func TestCompleters_SplitLinesLikeTheShell(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"quoted current word", `git checkout "feat`, "feature/api"},
		{"after &&", `git commit -m "fix bug" && git checkout ma`, "main"},
		{"after ;", `echo x; git checkout ma`, "main"},
		{"after ||", `false || git checkout ma`, "main"},
		{"inside $(", `echo $(git checkout ma`, "main"},
		{"inside backticks", "echo `git checkout ma", "main"},
		{"pipe inside quotes is not a pipe", `git log --grep="x|y" ma`, "main"},
		{"after a 2>&1 redirection", `git checkout 2>&1 ma`, "main"},
		{"after an assignment", `GIT_PAGER=cat git checkout ma`, "main"},
		{"after then", `if true; then git checkout ma`, "main"},
		{"after time", `time git checkout ma`, "main"},
		{"after an assignment from a substitution", `GIT_DIR=$(pwd)/.git git checkout ma`, "main"},
		{"after an assignment from backticks", "GIT_DIR=`pwd`/.git git checkout ma", "main"},
		{"after an assignment from arithmetic", `N=$((1 + 2)) git checkout ma`, "main"},
		{"after a heredoc body", "cat <<EOF\nfoo\nEOF\ngit checkout ma", "main"},
		{"after an assignment from an expansion with a blank", `GIT_DIR=${x// /_} git checkout ma`, "main"},
		{"after an assignment from an expansion with a paren", `GIT_DIR=${x:-(} git checkout ma`, "main"},
		{"after a substitution in a quoted assignment", `GIT_DIR="$(pwd)/.git" git checkout ma`, "main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := refsVCS().Complete(context.Background(), tt.line, len(tt.line))
			if len(result.Items) != 1 || result.Items[0].Value != tt.want {
				t.Fatalf("Complete(%q) = %#v, want %q", tt.line, result.Items, tt.want)
			}
		})
	}
}

// The & of a redirection (2>&1, >&2, &>) and the | of >| do not end the
// command: the word after them is still an argument, not a command name.
func TestExecutableCompleter_RedirectionAmpersandIsNotCommandPosition(t *testing.T) {
	exe := NewExecutableCompleter()
	exe.scanPath = func(string) []string { return []string{"1password", "tar"} }
	for _, line := range []string{"ls 2>&1", "make -k 2>&1 ta", "make &>log ta", "echo hi >| ta"} {
		result, _ := exe.Complete(context.Background(), line, len(line))
		if got := result.Outcome(); got != OutcomeNotMine {
			t.Errorf("Complete(%q) outcome = %v with items %#v, want not_mine (argument, not command)", line, got, result.Items)
		}
	}
}

// The ) or backtick that closes a substitution goes on with the word that
// holds it: the word after it is an argument of the outer command.
func TestExecutableCompleter_WordAfterASubstitutionIsAnArgument(t *testing.T) {
	exe := NewExecutableCompleter()
	exe.scanPath = func(string) []string { return []string{"fortune", "foo"} }
	for _, line := range []string{"echo $(date) fo", "echo `date` fo", "echo $((1+2)) fo", "cat <(ls) fo", "echo \"$(date)\" fo"} {
		result, _ := exe.Complete(context.Background(), line, len(line))
		if got := result.Outcome(); got != OutcomeNotMine {
			t.Errorf("Complete(%q) outcome = %v with items %#v, want not_mine (argument, not command)", line, got, result.Items)
		}
	}
	for _, line := range []string{"echo $(fo", "echo `fo", "cat <(fo", "echo $(date; fo"} {
		result, _ := exe.Complete(context.Background(), line, len(line))
		if len(result.Items) != 2 {
			t.Errorf("Complete(%q) = %#v, want both executables (command position inside)", line, result.Items)
		}
	}
}

// The word after > or 2> (or glued to it) is a file to write, whatever
// the command: refs, executables or a tool's arguments are not an answer.
func TestCompleters_LeaveARedirectionTargetToFiles(t *testing.T) {
	for _, line := range []string{"git checkout > ma", "git checkout >ma", "git checkout 2>> ma", "git checkout >| ma"} {
		if result, _ := refsVCS().Complete(context.Background(), line, len(line)); len(result.Items) != 0 {
			t.Errorf("VCS Complete(%q) = %#v, want nothing for a redirection target", line, result.Items)
		}
	}
	exe := NewExecutableCompleter()
	exe.scanPath = func(string) []string { return []string{"output-tool"} }
	for _, line := range []string{"> ou", "2> ou", "< ou"} {
		if result, _ := exe.Complete(context.Background(), line, len(line)); result.Outcome() != OutcomeNotMine {
			t.Errorf("executable Complete(%q) = %v %#v, want not mine for a redirection target", line, result.Outcome(), result.Items)
		}
	}
	for _, line := range []string{"kubectl get > po", "kubectl get >po"} {
		if _, ok := parseCobraRequest(line, len(line)); ok {
			t.Errorf("parseCobraRequest(%q) asks __complete for a redirection target", line)
		}
	}
	// The command after the redirection's target is still the command.
	if result, _ := exe.Complete(context.Background(), "> out ou", len("> out ou")); len(result.Items) != 1 {
		t.Errorf("executable Complete after a redirection target = %#v, want output-tool", result.Items)
	}
}

// An operator glued to the word (no space) is not part of the completed
// word: the replacement starts after it, so Tab keeps it.
func TestRouter_KeepsOperatorGluedToCompletedWord(t *testing.T) {
	exe := NewExecutableCompleter()
	exe.scanPath = func(string) []string { return []string{"grep"} }
	router := NewRouter()
	router.Register(exe, PriorityExecutable)

	line := "ls|gr"
	result, _ := router.Complete(context.Background(), line, len(line))
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v, want grep", result.Items)
	}
	if got := applied(line, result); got != "ls|grep" {
		t.Fatalf("applied %q = %q, want %q", line, got, "ls|grep")
	}
}

// A heredoc body is text for the command that reads it, not a command line:
// a word there is no command name and no argument, so only file names
// complete it.
func TestCompleters_HeredocBodyIsNotACommand(t *testing.T) {
	exe := NewExecutableCompleter()
	exe.scanPath = func(string) []string { return []string{"make", "man"} }
	for _, line := range []string{"cat <<EOF\nma", "cat <<EOF\n", "cat <<EOF\nfoo\nma", "cat <<EOF\nfoo ma"} {
		if result, _ := exe.Complete(context.Background(), line, len(line)); result.Outcome() != OutcomeNotMine {
			t.Errorf("executable Complete(%q) = %v %#v, want not mine in a heredoc body", line, result.Outcome(), result.Items)
		}
	}
	if result, _ := refsVCS().Complete(context.Background(), "git checkout <<EOF\nma", len("git checkout <<EOF\nma")); len(result.Items) != 0 {
		t.Errorf("VCS completed a heredoc body as an argument: %#v", result.Items)
	}
	// File names do complete in a body.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	body := "cat <<EOF\nsee " + dir + "/no"
	if result, _ := NewFileCompleter().Complete(context.Background(), body, len(body)); len(result.Items) != 1 || result.Items[0].Value != "notes.txt" {
		t.Errorf("file Complete(%q) = %#v, want notes.txt", body, result.Items)
	}
	// After the delimiter line, the next line is a command again.
	line := "cat <<EOF\nfoo\nEOF\nma"
	if result, _ := exe.Complete(context.Background(), line, len(line)); len(result.Items) != 2 {
		t.Errorf("executable Complete(%q) = %#v, want both executables after the body", line, result.Items)
	}
}

// A comment is text: no command reads its words, so no tool is asked to
// complete them (kubectl __complete for "kubectl get # po").
func TestCompleters_CommentIsNotArguments(t *testing.T) {
	for _, line := range []string{"kubectl get # po", "kubectl get #po", "kubectl # get "} {
		if _, ok := parseCobraRequest(line, len(line)); ok {
			t.Errorf("parseCobraRequest(%q) asks __complete for a comment", line)
		}
	}
}
