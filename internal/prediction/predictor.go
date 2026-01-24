package prediction

import (
	"strings"
	"time"
)

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

// PredictCommand predicts the next command based on the last command.
func (p *Predictor) PredictCommand(lastCmd, cwd string) string {
	if !p.config.Enabled || p.store == nil {
		return ""
	}

	// Normalize command (just the base command)
	lastCmd = normalizeCommand(lastCmd)

	seqs, err := p.store.GetSequences(lastCmd, cwd)
	if err != nil || len(seqs) == 0 {
		return ""
	}

	// Score and filter
	var best CommandSequence
	var bestScore float64

	for _, seq := range seqs {
		score := p.scoreSequence(seq)
		if score > bestScore && score >= p.config.ConfidenceThreshold {
			best = seq
			bestScore = score
		}
	}

	if bestScore >= p.config.ConfidenceThreshold {
		return best.NextCommand
	}
	return ""
}

// PredictPaths predicts paths for a command.
func (p *Predictor) PredictPaths(cmd, partial, cwd, prevCmd string) []ScoredPrediction {
	if !p.config.Enabled || p.store == nil {
		return nil
	}

	var predictions []ScoredPrediction
	seen := make(map[string]bool)

	// 1. Command-specific paths
	cmdPaths, _ := p.store.GetPathsForCommand(normalizeCommand(cmd), cwd)
	for _, pu := range cmdPaths {
		if seen[pu.Path] {
			continue
		}
		if partial != "" && !strings.HasPrefix(pu.Path, partial) {
			continue
		}
		if pu.Count >= p.config.PathMinCount {
			score := p.scorePathUsage(pu)
			predictions = append(predictions, ScoredPrediction{
				Text:        pu.Path,
				Score:       score,
				Source:      "command",
				IsPredicted: true,
			})
			seen[pu.Path] = true
		}
	}

	// 2. Sequence-based paths
	if prevCmd != "" {
		seqPaths, _ := p.store.GetPathsAfterCommand(normalizeCommand(prevCmd))
		for _, ps := range seqPaths {
			if seen[ps.Path] {
				continue
			}
			if partial != "" && !strings.HasPrefix(ps.Path, partial) {
				continue
			}
			score := p.scorePathSequence(ps)
			predictions = append(predictions, ScoredPrediction{
				Text:        ps.Path,
				Score:       score * 0.8, // Slightly lower weight for sequence-based
				Source:      "sequence",
				IsPredicted: true,
			})
			seen[ps.Path] = true
		}
	}

	SortPredictions(predictions)
	return predictions
}

// Record records a successful command for learning.
func (p *Predictor) Record(prevCmd, cmd, cwd string, paths []string) {
	if p.store == nil {
		return
	}

	// Record command sequence
	if prevCmd != "" {
		p.store.RecordSequence(
			normalizeCommand(prevCmd),
			normalizeCommand(cmd),
			cwd,
		)
	}

	// Record path usage
	baseCmd := normalizeCommand(cmd)
	for _, path := range paths {
		p.store.RecordPathUsage(baseCmd, path, cwd)
		if prevCmd != "" {
			p.store.RecordPathSequence(normalizeCommand(prevCmd), path)
		}
	}
}

func (p *Predictor) scoreSequence(seq CommandSequence) float64 {
	// Base score from count
	countScore := float64(seq.Count) / (float64(seq.Count) + 5.0)

	// Recency boost
	hoursSince := time.Since(seq.LastUsed).Hours()
	recencyScore := 1.0 / (1.0 + hoursSince/float64(p.config.PathRecencyHours))

	return countScore*0.7 + recencyScore*0.3
}

func (p *Predictor) scorePathUsage(pu PathUsage) float64 {
	countScore := float64(pu.Count) / (float64(pu.Count) + 3.0)
	hoursSince := time.Since(pu.LastUsed).Hours()
	recencyScore := 1.0 / (1.0 + hoursSince/float64(p.config.PathRecencyHours))
	return countScore*0.6 + recencyScore*0.4
}

func (p *Predictor) scorePathSequence(ps PathSequence) float64 {
	countScore := float64(ps.Count) / (float64(ps.Count) + 3.0)
	hoursSince := time.Since(ps.LastUsed).Hours()
	recencyScore := 1.0 / (1.0 + hoursSince/float64(p.config.PathRecencyHours))
	return countScore*0.6 + recencyScore*0.4
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
