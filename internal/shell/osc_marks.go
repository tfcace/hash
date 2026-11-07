package shell

import (
	"os"

	"github.com/tfcace/hash/internal/shell/integration"
)

// marksPhase is where the current prompt's OSC 133 record is.
type marksPhase int

const (
	marksNone   marksPhase = iota // before the first prompt
	marksPrompt                   // A sent: the prompt is drawn
	marksInput                    // B sent: the user is typing the line
	marksRan                      // the line was entered and ran
)

// promptMarks emits the OSC 133 marks for each prompt, in order: A when the
// prompt starts, B once when its input is ready (not for the picker's
// re-runs, nor the editors a ?? turn opens), and D before the next A, with
// the line's exit code only when a line ran. C, where the output begins, is
// the executor's (it marks non-PTY commands). Safe on a nil emitter.
type promptMarks struct {
	osc   *integration.Emitter
	phase marksPhase
}

// promptStart closes the previous prompt's record and opens the next one.
func (m *promptMarks) promptStart(exitCode int) {
	if m.osc == nil {
		return
	}
	switch m.phase {
	case marksRan:
		m.osc.CommandFinished(exitCode)
	case marksPrompt, marksInput:
		m.osc.CommandAbandoned() // empty line or Ctrl+C: nothing ran
	}
	m.osc.PromptStart()
	if cwd, err := os.Getwd(); err == nil {
		m.osc.ReportDirectory(cwd)
	}
	m.phase = marksPrompt
}

// inputReady marks where the prompt's input begins, once per prompt.
func (m *promptMarks) inputReady() {
	if m.osc == nil || m.phase != marksPrompt {
		return
	}
	m.osc.CommandStart()
	m.phase = marksInput
}

// lineRan says the prompt's line was entered and is running.
func (m *promptMarks) lineRan() {
	if m.phase != marksNone {
		m.phase = marksRan
	}
}
