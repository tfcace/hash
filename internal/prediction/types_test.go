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

func TestPathUsage_Fields(t *testing.T) {
	pu := PathUsage{
		Command:  "vim",
		Path:     "src/main.go",
		Cwd:      "/home/user/project",
		Count:    10,
		LastUsed: time.Now(),
	}

	if pu.Command != "vim" {
		t.Errorf("Command = %q, want %q", pu.Command, "vim")
	}
}

func TestScoredPrediction_Ordering(t *testing.T) {
	predictions := []ScoredPrediction{
		{Text: "npm test", Score: 0.8},
		{Text: "npm install", Score: 0.5},
		{Text: "npm run build", Score: 0.9},
	}

	// Sort by score descending
	SortPredictions(predictions)

	if predictions[0].Text != "npm run build" {
		t.Errorf("First should be highest score, got %q", predictions[0].Text)
	}
}
