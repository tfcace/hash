package shell

import "time"

// lineStatus is how the user's last line ended: what the prompt colors,
// OSC 133 D reports, $? holds and exit or Ctrl+D ends the shell with. Every
// line the user runs sets it (finishShellLine, finishAgentLine, !!), never
// hooks, pipe captures or startup commands, which are not the user's line.
type lineStatus struct {
	exitCode int
	duration time.Duration
}

// commandOutcome is the user's last command: a shell command or a ??
// request. It is what a bare ?? explains, the predictor follows, the
// learning loop observes and an issue report quotes. It is set whole, once
// per command line the user runs (typed, accepted from the agent, a
// builtin, or a ?? request). !! is a line but no command: it sets the
// line's status and leaves this as it is.
type commandOutcome struct {
	command  string // the line, "" before the first
	kind     lineKind
	exitCode int
	duration time.Duration
	stderr   string // what went wrong, for learning and a bare ??
	cwd      string // where it ran
	id       int64  // its history id, 0 when history did not record it
	typoFix  string // the line with its mistyped command names corrected, "" when there is none
}

// lineKind is what the user's line was.
type lineKind int

const (
	shellLine lineKind = iota // a shell command: history, learning and the predictor follow it
	agentLine                 // a ?? request: history records it once its turn is over
)

// failedCommand is the shell command a bare ?? explains: the last line, when
// it was a shell command that failed.
func (o commandOutcome) failedCommand() *LastError {
	if o.kind != shellLine || o.command == "" || o.exitCode == 0 {
		return nil
	}
	return &LastError{Command: o.command, Stderr: o.stderr, ExitCode: o.exitCode}
}

// status is the line status of the line o is the outcome of.
func (o commandOutcome) status() lineStatus {
	return lineStatus{exitCode: o.exitCode, duration: o.duration}
}

// shellCommand is the last command when it was a shell command, for the
// predictor's command sequences; a ?? request breaks the sequence.
func (o commandOutcome) shellCommand() string {
	if o.kind != shellLine {
		return ""
	}
	return o.command
}
