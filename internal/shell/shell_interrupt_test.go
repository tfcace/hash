package shell

import (
	"bytes"
	"testing"
)

// Ctrl+C during a ?? turn reaches the turn and cancels its request; once
// the turn ends, Ctrl+C is the shell's again.
func TestTurn_CtrlCCancelsTheRequestUntilTheTurnEnds(t *testing.T) {
	sh := &Shell{
		agentOutput: NewAgentOutputCoordinator(&bytes.Buffer{}),
		responseUI:  NewResponseUI(&bytes.Buffer{}),
	}
	turn := sh.beginTurn(t.Context())

	sh.interrupts.fire()
	if turn.ctx.Err() == nil {
		t.Fatal("Ctrl+C did not cancel the turn's request")
	}
	turn.end()
	if sh.turn.Load() != nil {
		t.Error("the ended turn is still the shell's live turn")
	}

	next := sh.beginTurn(t.Context())
	defer next.end()
	turn.release() // a stale release must not drop the newer turn's claim
	sh.interrupts.fire()
	if next.ctx.Err() == nil {
		t.Error("Ctrl+C did not reach the newer turn")
	}
}
