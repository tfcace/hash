// internal/compat/builtins_test.go
package compat

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNoopBuiltins_List(t *testing.T) {
	for _, name := range []string{"bindkey", "setopt", "unsetopt", "autoload", "compdef", "zstyle"} {
		if noopBuiltins[name] == "" {
			t.Errorf("expected %q in noopBuiltins", name)
		}
	}
}

// A typeset or declare line the filter keeps must run: every flag is tried
// through the interpreter, and the filter keeps exactly the lines whose
// flags it accepts (mvdan's declare rejects -i, -l, +x and others bash has).
func TestTypesetSkipReason_MatchesTheInterpreter(t *testing.T) {
	for _, cmd := range []string{"typeset", "declare"} {
		for _, sign := range []string{"-", "+"} {
			for c := byte('A'); c <= 'z'; c++ {
				if c > 'Z' && c < 'a' {
					continue
				}
				line := cmd + " " + sign + string(c) + " v"
				_, status := runStatus(t, line+"\n")
				kept := typesetSkipReason(line) == ""
				if kept != (status != 2) { // 2: invalid option; -f and -p exit 1 for a missing v
					t.Errorf("%q: kept = %v, but it exits %d", line, kept, status)
				}
			}
		}
	}
	if reason := typesetSkipReason("typeset -gx A=1 -i n=5"); reason != "typeset flag -i that hash does not support" {
		t.Errorf("reason = %q", reason)
	}
	if reason := typesetSkipReason("typeset -U path"); reason != "zsh-only typeset flag -U" {
		t.Errorf("reason = %q", reason)
	}
	for _, line := range []string{`typeset x="a -i"`, "declare -gx A=1"} {
		if reason := typesetSkipReason(line); reason != "" {
			t.Errorf("%q skipped (%s), want it kept", line, reason)
		}
	}
}

// The interpreter's declare takes no --: it declares nothing and fails with
// "invalid name", so a typeset or declare with -- is skipped, not kept.
func TestTypesetSkipReason_DoubleDash(t *testing.T) {
	for _, line := range []string{"typeset -- x=1", "declare -- x=1", "typeset -g -- x=1"} {
		if out, _ := runStatus(t, line+"\necho \"x=$x\"\n"); out == "x=1\n" {
			t.Errorf("%q: the interpreter declares x, it need not be skipped", line)
		}
		if reason := typesetSkipReason(line); reason == "" {
			t.Errorf("%q kept, the interpreter rejects it", line)
		}
	}
}

// A file written for another shell (a migrated .bashrc, .zshrc) that execs
// a shell would leave hash for good at startup: the exec is skipped and
// reported. hash's own
// .hashrc keeps it: exec there replaces hash, as in bash.
func TestFilter_SkipsAnExecOfAnotherShell(t *testing.T) {
	const execs = "export A=1\nexec zsh\n[ -x /bin/fish ] && exec fish -l\nif true; then\n  exec \"$SHELL\" -l\nfi\nexec -a -bash /bin/bash\nexec ls\nexport B=1\n"
	dir := t.TempDir()
	for _, tt := range []struct {
		name, shell, text string
		want              int
	}{
		{".bashrc", "bash", execs, 4},
		{".zshrc", "zsh", execs, 4},
		{".hashrc", "", execs, 0},
	} {
		path := filepath.Join(dir, tt.name)
		writeFile(t, path, tt.text)
		filtered, report, err := FilterWithDialect(path, tt.shell, "bash")
		if err != nil {
			t.Fatal(err)
		}
		var skipped []string
		for _, item := range report.SkippedItems {
			if strings.Contains(item.Reason, "exec of another shell") {
				skipped = append(skipped, item.Content)
			}
		}
		if len(skipped) != tt.want || !strings.Contains(filtered, "exec ls") {
			t.Errorf("%s: skipped %q, want %d execs of a shell skipped and exec ls kept\n%s", tt.name, skipped, tt.want, filtered)
		}
	}
}
