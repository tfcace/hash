package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A job's commands lead process groups of their own, so they are not the
// terminal's foreground: the hangup the terminal sends its foreground as
// the session's leader (hash) ends, at exit or Ctrl+D, misses them, and
// they run on, as bash -i leaves its jobs. They shared hash's group and
// died of it.
func TestInteractive_ExitLeavesJobsRunning(t *testing.T) {
	t.Parallel()
	for _, end := range []string{"exit\r", "\x04"} {
		rec := filepath.Join(t.TempDir(), "rec")
		s := startTTY(t)
		time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
		s.typeString(`sh -c 'trap "echo hup >> ` + rec + `; exit" HUP; echo ready >> ` + rec + `; sleep 1; echo end >> ` + rec + `' &` + "\r")
		waitForFile(t, rec, "ready", 5*time.Second)
		s.typeString(end)
		s.wait(5 * time.Second)
		if got := waitForFile(t, rec, "end", 3*time.Second); got != "ready\nend\n" {
			t.Errorf("%q: the job recorded %q, want ready, then end (no hangup)", end, got)
		}
	}
}

// A job that reads the terminal gets an error (EIO: it is not in the
// terminal's foreground and ignores SIGTTIN), as bash's job is stopped,
// instead of taking the keys typed at the prompt; so does a read the job's
// shell code makes, which hash runs. A job's stty works (SIGTTOU ignored,
// as the job could never be continued if it stopped).
func TestInteractive_JobsAndTheTerminal(t *testing.T) {
	t.Parallel()
	rec := filepath.Join(t.TempDir(), "rec")
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	s.typeString(`sh -c 'read x </dev/tty; echo "read=$? x=$x" >> ` + rec + `' &` + "\r")
	if got := waitForFile(t, rec, "read=", 3*time.Second); got != "read=1 x=\n" {
		t.Errorf("the job's command recorded %q, want a failed read", got)
	}
	s.typeString(`{ read y </dev/tty; echo "hread=$?" >> ` + rec + `; stty -echo </dev/tty; echo "stty=$?" >> ` + rec + `; stty echo </dev/tty; } 2>/dev/null &` + "\r")
	if got := waitForFile(t, rec, "stty=", 3*time.Second); !strings.HasSuffix(got, "hread=1\nstty=0\n") {
		t.Errorf("the job recorded %q, want a failed read, then stty=0", got)
	}
	from := len(s.output())
	s.typeString("echo typed-$((1+1))\r")
	s.waitFor(from, "typed-2", 5*time.Second)
	s.typeString("exit\r")
	s.wait(5 * time.Second)
}

// `kill 0` in a job signals the job's own group, not the shell's: the
// foreground command running meanwhile survives it.
func TestInteractive_KillZeroInAJobStaysInIt(t *testing.T) {
	t.Parallel()
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	from := len(s.output())
	s.typeString("sh -c 'sleep 0.3; kill 0' & sleep 1 </dev/null; echo fg=$?\r")
	s.waitFor(from, "fg=0", 5*time.Second) // fg=143: the job's kill 0 reached the foreground
	s.typeString("exit\r")
	s.wait(5 * time.Second)
}
