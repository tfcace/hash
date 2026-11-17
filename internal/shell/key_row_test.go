package shell

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/markdown"
	"github.com/tfcace/hash/internal/prompt"
)

const codeRow = markdown.CodeLinePrefix + "kubectl rollout undo deploy/api" + ansiReset + "\n"

// keyRow is what ShowHints draws after reply was streamed.
func keyRow(t *testing.T, ct ConfirmationType, reply ...string) string {
	t.Helper()
	withColumns(t, 100)
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	for _, chunk := range reply {
		aoc.WriteStream(chunk)
	}
	buf.Reset()
	aoc.ShowHints(ct)
	return buf.String()
}

// A reply that ends in a code block is a frame still open: its keys close
// it. Prose has no frame, so its keys stay a row of their own.
func TestKeyRow_ClosesACodeBlockTheReplyEndsIn(t *testing.T) {
	want := frameGray + "└─ enter to run · tab to edit · esc to cancel" + ansiReset + "\n"
	if got := keyRow(t, ConfirmTypeCommand, "Roll it back:\n", codeRow); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestKeyRow_StaysAPlainRowUnderProse(t *testing.T) {
	want := frameGray + "  agent · enter to finish · tab to copy · r to reply" + ansiReset + "\n"
	for name, reply := range map[string][]string{
		"prose":           {"A monad wraps values.\n"},
		"code then prose": {codeRow, "That undoes the rollout.\n"},
		"nothing":         nil,
	} {
		if got := keyRow(t, ConfirmTypeExplanation, reply...); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestKeyRow_SeesACodeRowHoweverItWasChunked(t *testing.T) {
	got := keyRow(t, ConfirmTypeCommand, codeRow[:3], codeRow[3:9], codeRow[9:])
	if !strings.Contains(got, "└─ enter to run") {
		t.Errorf("got %q", got)
	}
}

func TestKeyRow_ABlankRowAfterTheCodeDoesNotOpenTheFrameAgain(t *testing.T) {
	if got := keyRow(t, ConfirmTypeCommand, codeRow, "\n"); !strings.Contains(got, "└─ enter to run") {
		t.Errorf("got %q", got)
	}
}

func TestKeyRow_AToolRowAfterTheCodeEndsItsFrame(t *testing.T) {
	withColumns(t, 100)
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.WriteStream(codeRow)
	aoc.RenderToolResult(agent.ToolCallUpdate{Kind: "execute", Title: "ls", Status: agent.ToolCallCompleted}, "")
	buf.Reset()
	aoc.ShowHints(ConfirmTypeExplanation)
	if got := buf.String(); strings.Contains(got, "└─") {
		t.Errorf("the keys closed a frame a tool row had already ended: %q", got)
	}
}

func TestKeyRow_ANewTurnForgetsTheLastOnesCode(t *testing.T) {
	withColumns(t, 100)
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.WriteStream(codeRow)
	aoc.BeginTurn()
	buf.Reset()
	aoc.ShowHints(ConfirmTypeExplanation)
	if got := buf.String(); strings.Contains(got, "└─") {
		t.Errorf("got %q", got)
	}
}

// A tool row's mark is a status: it takes the theme's status colors.
func TestToolRow_MarksUseTheThemesStatusColors(t *testing.T) {
	setColorProfile(t, colorprofile.TrueColor)
	withColumns(t, 100)
	for name, tt := range map[string]struct {
		update agent.ToolCallUpdate
		want   string
	}{
		"done":   {agent.ToolCallUpdate{Kind: "execute", Title: "ls", Status: agent.ToolCallCompleted}, bannerSuccess + "✓"},
		"failed": {agent.ToolCallUpdate{Kind: "execute", Title: "ls", Status: agent.ToolCallFailed}, bannerError + "✗"},
		"denied": {agent.ToolCallUpdate{Kind: "execute", Title: "ls", Status: agent.ToolCallFailed, PermissionDenied: true}, bannerWarning + "–"},
	} {
		var buf bytes.Buffer
		aoc := NewAgentOutputCoordinator(&buf)
		aoc.SetPaletteFunc(bannerPalette)
		aoc.RenderToolResult(tt.update, "")
		if !strings.Contains(buf.String(), tt.want) {
			t.Errorf("%s: no %q in %q", name, tt.want, buf.String())
		}
	}
}

func TestToolRow_WithoutAPaletteUsesTheDefaultOne(t *testing.T) {
	setColorProfile(t, colorprofile.TrueColor)
	var buf bytes.Buffer
	NewAgentOutputCoordinator(&buf).RenderToolResult(agent.ToolCallUpdate{Kind: "execute", Title: "ls", Status: agent.ToolCallCompleted}, "")
	if want := fgCode(prompt.DefaultPalette().Success) + "✓"; !strings.Contains(buf.String(), want) {
		t.Errorf("no %q in %q", want, buf.String())
	}
}

// An error row follows the reply on a failed turn: the retry keys sit under
// it, not under the code block above it.
func TestKeyRow_AFailedTurnsKeysDoNotCloseACodeBlock(t *testing.T) {
	got := keyRow(t, ConfirmTypeError, codeRow)
	if strings.Contains(got, "└─") || !strings.Contains(got, "enter to retry") {
		t.Errorf("got %q", got)
	}
}

// keyRowAfter is the key row after each step ran on a fresh coordinator.
func keyRowAfter(t *testing.T, steps ...func(*AgentOutputCoordinator)) string {
	t.Helper()
	withColumns(t, 100)
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	for _, step := range steps {
		step(aoc)
	}
	buf.Reset()
	aoc.ShowHints(ConfirmTypeCommand)
	return buf.String()
}

func stream(text string) func(*AgentOutputCoordinator) {
	return func(aoc *AgentOutputCoordinator) { aoc.WriteStream(text) }
}

func takeRow(aoc *AgentOutputCoordinator) { aoc.TakeRow() }

// A row ended by something taking it is over: it must not be mistaken for
// the start of the next one.
func TestKeyRow_ARowTakenMidwayIsNotTheNextRowsStart(t *testing.T) {
	if got := keyRowAfter(t, stream("an unfinished line of prose"), takeRow, stream(codeRow)); !strings.Contains(got, "└─") {
		t.Errorf("code after a taken prose row: %q", got)
	}
	unfinishedCode := strings.TrimSuffix(codeRow, "\n")
	if got := keyRowAfter(t, stream(unfinishedCode), takeRow, stream("Then prose.\n")); strings.Contains(got, "└─") {
		t.Errorf("prose after a taken code row: %q", got)
	}
}

func TestKeyRow_ACodeRowEndedByTheReplysEndStillClosesWithTheKeys(t *testing.T) {
	if got := keyRowAfter(t, stream(strings.TrimSuffix(codeRow, "\n")), takeRow); !strings.Contains(got, "└─") {
		t.Errorf("got %q", got)
	}
}
