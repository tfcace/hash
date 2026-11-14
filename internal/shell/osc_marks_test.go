package shell

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/shell/integration"
)

var osc133Re = regexp.MustCompile(`\x1b\]133;([^\x07]*)\x07`)

func osc133Marks(out string) string {
	var marks []string
	for _, m := range osc133Re.FindAllStringSubmatch(out, -1) {
		marks = append(marks, m[1])
	}
	return strings.Join(marks, " ")
}

// Each prompt is one A, one B, and a D before the next A: with the exit
// code when a line ran, without one for an empty line or Ctrl+C. Before,
// every editor sent B (the picker's re-runs, the ?? turn's editors) and D
// repeated the stale exit code after an empty line.
func TestPromptMarks_OnePromptRecordPerPrompt(t *testing.T) {
	var buf bytes.Buffer
	m := promptMarks{osc: integration.NewWithWriter(&buf)}

	m.promptStart(0)
	m.inputReady()
	m.inputReady() // the Ctrl+R picker re-runs the prompt's editor
	m.lineRan()
	m.inputReady() // the ?? turn's inline editor
	m.promptStart(3)
	m.inputReady()
	m.promptStart(3) // the line was empty
	m.inputReady()
	m.promptStart(3) // Ctrl+C at the prompt

	if got, want := osc133Marks(buf.String()), "A B D;3 A B D A B D A"; got != want {
		t.Errorf("marks = %q, want %q", got, want)
	}
}

func TestPromptMarks_NilEmitterIsSilent(t *testing.T) {
	var m promptMarks
	m.promptStart(0)
	m.inputReady()
	m.lineRan()
}

// The REPL marks a line as run only when one was entered.
func TestHandleLine_MarksOnlyEnteredLines(t *testing.T) {
	var buf bytes.Buffer
	sh := &Shell{marks: promptMarks{osc: integration.NewWithWriter(&buf)}}
	sh.marks.promptStart(0)
	sh.marks.inputReady()
	sh.handleLine(t.Context(), "")
	sh.marks.promptStart(0)
	if got, want := osc133Marks(buf.String()), "A B D A"; got != want {
		t.Errorf("marks = %q, want %q", got, want)
	}
}
