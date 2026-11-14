package main

import (
	"os/exec"
	"testing"
	"time"
)

// A pager or an editor at a pipeline's tail reads its keys from the
// terminal: the tail gets the terminal itself for its stdout and stderr,
// which macOS less and vim read keys from when stdin is the pipe. Its
// stderr used to be a PTY, which nobody types into: the pager never saw q
// and the shell hung.
func TestInteractive_PagerAtAPipelinesTailReadsTheTerminal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, prog, line, drawn, quit string
	}{
		{"less", "less", "printf 'line-%s\\n' a b c | less\r", "(END)", "q"},
		{"vim", "vim", "echo hi | vim -u NONE -i NONE -n -\r", "hi", ":q!\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.prog); err != nil {
				t.Skipf("%s not installed", tc.prog)
			}
			s := startTTYWith(t, []string{"TERM=xterm-256color", "LESS=", "LESSHISTFILE=-"})
			time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
			from := len(s.output())
			s.typeString(tc.line)
			s.waitFor(from, tc.drawn, 5*time.Second)
			time.Sleep(300 * time.Millisecond) // the program waits for keys
			from = len(s.output())
			s.typeString(tc.quit)
			s.waitFor(from, "\x1b]133;D;0", 5*time.Second) // the line ended
			from = len(s.output())
			s.typeString("echo back-$((40+2))\r")
			s.waitFor(from, "back-42", 5*time.Second)
			s.typeString("exit\r")
			s.wait(5 * time.Second)
		})
	}
}

// A pipeline's command that reads the terminal itself (a password prompt
// on /dev/tty: sudo, ssh, gpg) gets the keys typed, as in any shell: hash
// leaves the terminal to the pipeline while it runs. A PTY for the tail,
// with the keys copied into it, took them away and hung the line.
func TestInteractive_PipelineHeadReadsTheTerminal(t *testing.T) {
	t.Parallel()
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	from := len(s.output())
	s.typeString("sh -c 'printf ask; read x </dev/tty; echo got-$x' | cat\r")
	s.waitFor(from, "ask", 5*time.Second)
	s.typeString("abc\r")
	s.waitFor(from, "got-abc", 5*time.Second)
	s.typeString("exit\r")
	s.wait(5 * time.Second)
}
