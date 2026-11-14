package prediction

import (
	"testing"
	"time"
)

func TestCommandSequence_Fields(t *testing.T) {
	seq := CommandSequence{
		PrevCommand: "git pull",
		NextCommand: "npm test",
		CwdPattern:  "/home/user/project",
		Count:       5,
		LastUsed:    time.Now(),
	}

	if seq.PrevCommand != "git pull" {
		t.Errorf("PrevCommand = %q, want %q", seq.PrevCommand, "git pull")
	}
}
