package prediction

import "strings"

// Predictor generates predictions based on history patterns.
type Predictor struct {
	store  *Store
	config Config
}

// NewPredictor creates a new predictor.
func NewPredictor(store *Store, config Config) *Predictor {
	return &Predictor{
		store:  store,
		config: config,
	}
}

// Close closes the underlying store.
func (p *Predictor) Close() error {
	if p.store != nil {
		return p.store.Close()
	}
	return nil
}

// PredictCommand predicts the next command based on the last command.
func (p *Predictor) PredictCommand(lastCmd, cwd string) string {
	if !p.config.Enabled || p.store == nil {
		return ""
	}

	// Normalize command (just the base command)
	lastCmd = normalizeCommand(lastCmd)

	seqs, err := p.store.RankSequences(lastCmd, cwd, p.config.RecencyWindowHours, 1)
	if err != nil || len(seqs) == 0 || seqs[0].Score < p.config.ConfidenceThreshold {
		return ""
	}
	return seqs[0].NextCommand
}

// Record records a successful command for learning: the full command is
// stored so that "echo world" is suggested, not just "echo".
func (p *Predictor) Record(prevCmd, cmd, cwd string) {
	if p.store == nil || prevCmd == "" {
		return
	}
	_ = p.store.RecordSequence(normalizeCommand(prevCmd), cmd, cwd)
}

// normalizeCommand extracts the base command from a full command line.
func normalizeCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
