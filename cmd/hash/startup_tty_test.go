package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startupHome is a home for an interactive hash with files (name to
// text) in it, past the welcome, and the environment that runs hash there
// as a person whose login shell was shell.
func startupHome(t *testing.T, shell string, files map[string]string) []string {
	t.Helper()
	home := interactiveHome(t)
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return append(isolatedEnv(home), "SHELL="+shell)
}

// A migrated zshrc that sources a plugin: the plugin is filtered as the
// zshrc is, so its alias loads, its zsh-only line is skipped quietly, and
// its setup builtins and version check run as zsh's would.
func TestInteractive_MigratedZshrcSourcesAPlugin(t *testing.T) {
	t.Parallel()
	env := startupHome(t, "/bin/zsh", map[string]string{
		".zshrc": "source ~/plugin.zsh\nexport FROM_RC=1\n",
		"plugin.zsh": "setopt extended_glob\nalias pk='echo plugin-alias'\nuser=${(%):-%n}\n" +
			"autoload -Uz is-at-least\nis-at-least 5.1 && echo plugin-modern\nbindkey -e; zstyle ':x' y z\n",
	})
	s := startTTYWith(t, env)
	s.waitFor(0, "Load these settings now?", 10*time.Second)
	s.answer("y\r")
	s.waitFor(0, "plugin-modern", 10*time.Second)
	from := len(s.output())
	s.typeString("pk; echo \"rc=$FROM_RC\"\r")
	s.waitFor(from, "rc=1", 10*time.Second)
	out := s.output()
	if !strings.Contains(out[from:], "plugin-alias") {
		t.Errorf("the plugin's alias did not load\nterminal: %q", out)
	}
	for _, bad := range []string{"not found", "syntax", "zsh feature"} {
		if strings.Contains(out, bad) {
			t.Errorf("terminal shows %q\nterminal: %q", bad, out)
		}
	}
	s.typeString("exit\r")
	s.wait(10 * time.Second)
}

// The Debian .bashrc's preamble, which returns from a shell that is not
// interactive, and [[ -o interactive ]] || return let the rest of the
// migrated .bashrc run in the interactive shell, with no error; a return
// later ends the file there.
func TestInteractive_StartupGuardsForAnInteractiveShell(t *testing.T) {
	t.Parallel()
	env := startupHome(t, "/bin/bash", map[string]string{
		".bashrc": "# If not running interactively, don't do anything\ncase $- in\n    *i*) ;;\n      *) return;;\nesac\n" +
			"[[ -o interactive ]] || return\nalias ll='echo listed'\nexport RC_DONE=1\n" +
			"[ -n \"$NO_SUCH_VAR\" ] || return\necho NOT-REACHED\n",
	})
	s := startTTYWith(t, env)
	s.waitFor(0, "Load these settings now?", 10*time.Second)
	s.answer("y\r")
	s.waitFor(0, "Loaded from", 10*time.Second)
	time.Sleep(500 * time.Millisecond) // the prompt
	from := len(s.output())
	s.typeString("ll; echo \"done=$RC_DONE\"\r")
	s.waitFor(from, "done=1", 10*time.Second)
	out := s.output()
	if !strings.Contains(out[from:], "listed") {
		t.Errorf("the .bashrc's alias did not load\nterminal: %q", out)
	}
	for _, bad := range []string{"NOT-REACHED", "return:", "can only be done"} {
		if strings.Contains(out, bad) {
			t.Errorf("terminal shows %q\nterminal: %q", bad, out)
		}
	}
	s.typeString("exit\r")
	s.wait(10 * time.Second)
}
