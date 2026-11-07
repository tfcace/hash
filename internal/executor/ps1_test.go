package executor

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The interactive shell has a PS1, as bash's does, so a startup file's
// `[ -z "$PS1" ] && return` (the old Debian .bashrc) reads it as
// interactive. It is a shell variable: commands do not inherit it. A
// non-interactive shell has none, and a PS1 already set stays as it is.
func TestExecute_InteractiveShellHasAPS1(t *testing.T) {
	keepEnv(t, "PS1") // the preset is exported: the process's own
	// Inspect the child's environment without starting another shell: dash
	// initializes its own PS1 even when it inherited none.
	line := `[ -z "$PS1" ] && echo none || echo set; env`
	for _, tc := range []struct {
		interactive bool
		preset      string
		want        string
	}{
		{true, "", "set"},
		{false, "", "none"},
		{true, "mine> ", "set"},
	} {
		e := New()
		if tc.preset != "" {
			e.env.setExportedString("PS1", tc.preset)
		}
		if tc.interactive {
			e.UseInteractiveTerminal()
		}
		var out bytes.Buffer
		if _, err := e.Execute(context.Background(), line, &out, &out); err != nil {
			t.Fatal(err)
		}
		got, childEnv, _ := strings.Cut(out.String(), "\n")
		if got != tc.want {
			t.Errorf("interactive=%v: %q, want %q", tc.interactive, got, tc.want)
		}
		childPS1, exported := "", false
		for line := range strings.SplitSeq(childEnv, "\n") {
			if value, ok := strings.CutPrefix(line, "PS1="); ok {
				childPS1, exported = value, true
			}
		}
		if exported != (tc.preset != "") || childPS1 != tc.preset {
			t.Errorf("child PS1 = %q (exported %v), want preset %q only", childPS1, exported, tc.preset)
		}
		if tc.preset != "" {
			out.Reset()
			if _, err := e.Execute(context.Background(), `printf '%s|' "$PS1"`, &out, &out); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.preset+"|" {
				t.Errorf("a PS1 already set became %q, want %q", out.String(), tc.preset)
			}
		}
	}
}
