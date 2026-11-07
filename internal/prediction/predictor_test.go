package prediction

import (
	"path/filepath"
	"testing"
)

func TestPredictor_PredictCommand(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(filepath.Join(tmpDir, "prediction.db"))
	defer store.Close()

	predictor := NewPredictor(store, DefaultConfig())

	// Build up history using predictor.Record for proper normalization
	for i := 0; i < 5; i++ {
		predictor.Record("git pull", "npm test", "/project")
	}
	predictor.Record("git pull", "npm install", "/project")

	prediction := predictor.PredictCommand("git pull", "/project")

	if prediction == "" {
		t.Fatal("Should return a prediction")
	}
	if prediction != "npm test" {
		t.Errorf("Prediction = %q, want %q", prediction, "npm test")
	}
}

func TestPredictor_NoClient(t *testing.T) {
	predictor := NewPredictor(nil, DefaultConfig())

	// Should not panic, just return empty
	prediction := predictor.PredictCommand("git pull", "/project")
	if prediction != "" {
		t.Errorf("Expected empty prediction with nil store, got %q", prediction)
	}
}

func TestPredictor_Disabled(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(filepath.Join(tmpDir, "prediction.db"))
	defer store.Close()

	// Use a temporary predictor to record data
	tempPredictor := NewPredictor(store, DefaultConfig())
	for i := 0; i < 5; i++ {
		tempPredictor.Record("git pull", "npm test", "/project")
	}

	cfg := DefaultConfig()
	cfg.Enabled = false
	predictor := NewPredictor(store, cfg)

	prediction := predictor.PredictCommand("git pull", "/project")
	if prediction != "" {
		t.Errorf("Expected empty prediction when disabled, got %q", prediction)
	}
}
