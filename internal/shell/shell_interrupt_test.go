package shell

import (
	"bytes"
	"testing"
)

func TestHandleAgentInterrupt_CancelsFullRequest(t *testing.T) {
	sh := &Shell{
		agentOutput: NewAgentOutputCoordinator(&bytes.Buffer{}),
	}

	agentCanceled := false
	exitSignalLoop := sh.handleAgentInterrupt(func() {
		agentCanceled = true
	})

	if !exitSignalLoop {
		t.Fatal("expected signal loop to exit after full cancel")
	}
	if !agentCanceled {
		t.Fatal("expected full agent cancel")
	}
}
