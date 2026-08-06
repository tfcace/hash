package plugin

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxCorrections     = 5
	maxCorrectionBytes = 16 * 1024
	maxSuggestionBytes = 16 * 1024
)

// EditorSuggestParams is the request sent while creating a prompt or after
// the user edits the current line. Previous is omitted before the first
// command in a session.
type EditorSuggestParams struct {
	Generation uint64                  `json:"generation"`
	Trigger    string                  `json:"trigger"`
	Line       string                  `json:"line"`
	Cursor     int                     `json:"cursor"`
	CWD        string                  `json:"cwd"`
	Dialect    string                  `json:"dialect"`
	Previous   *PreviousCommandOutcome `json:"previous,omitempty"`
}

// PreviousCommandOutcome identifies the last command visible to the editor.
type PreviousCommandOutcome struct {
	Line     string `json:"line"`
	CWD      string `json:"cwd,omitempty"`
	ExitCode int    `json:"exit_code"`
	Canceled bool   `json:"canceled"`
}

// EditorSuggestResult is the complete command line returned by a suggestion
// provider. Hash renders only the suffix after validating the full line.
type EditorSuggestResult struct {
	Text string `json:"text"`
}

// CommandFinishedParams is the canonical protocol-v1 command outcome.
type CommandFinishedParams struct {
	Generation          uint64 `json:"generation"`
	OriginalLine        string `json:"original_line"`
	ExecutedLine        string `json:"executed_line"`
	ExitCode            int    `json:"exit_code"`
	DurationMS          int64  `json:"duration_ms"`
	FailureKind         string `json:"failure_kind"`
	ErrorMessage        string `json:"error_message"`
	StdoutTail          string `json:"stdout_tail"`
	StderrTail          string `json:"stderr_tail"`
	OutputStreamsMerged bool   `json:"output_streams_merged"`
	CWD                 string `json:"cwd"`
	Dialect             string `json:"dialect"`
	Canceled            bool   `json:"canceled"`
}

// CommandFinishedResult is returned by command.finished.
type CommandFinishedResult struct {
	Corrections []string `json:"corrections"`
}

// HistoryQueryParams requests bounded successful history.
type HistoryQueryParams struct {
	ParentRequestID int64  `json:"parent_request_id"`
	Prefix          string `json:"prefix,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	Limit           int    `json:"limit"`
}

type HistoryEntry struct {
	Line      string `json:"line"`
	CWD       string `json:"cwd"`
	ExitCode  int    `json:"exit_code"`
	Timestamp string `json:"timestamp"`
}

type HistoryQueryResult struct {
	Entries []HistoryEntry `json:"entries"`
}

type CompletionQueryParams struct {
	ParentRequestID int64  `json:"parent_request_id"`
	Line            string `json:"line"`
	Cursor          int    `json:"cursor"`
}

type CompletionItem struct {
	Label      string `json:"label"`
	InsertText string `json:"insert_text"`
}

type CompletionQueryResult struct {
	Items []CompletionItem `json:"items"`
}

// ValidateCorrections performs language-neutral safety checks. Shell syntax
// validation is applied by the shell before exposing candidates to the editor.
func ValidateCorrections(executed string, candidates []string) ([]string, error) {
	if len(candidates) > maxCorrections {
		return nil, fmt.Errorf("too many corrections: %d", len(candidates))
	}
	seen := make(map[string]struct{}, len(candidates))
	valid := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == executed || candidate == "" || len(candidate) > maxCorrectionBytes || !utf8.ValidString(candidate) {
			continue
		}
		if strings.ContainsAny(candidate, "\r\n") || strings.IndexFunc(candidate, unicode.IsControl) >= 0 {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		valid = append(valid, candidate)
	}
	return valid, nil
}

// ValidateSuggestionCandidate performs protocol-level checks shared by all
// editor.suggest providers. Dialect parsing is performed by Hash after this
// language-neutral validation succeeds.
func ValidateSuggestionCandidate(input, candidate string) error {
	if candidate == "" {
		return nil
	}
	if candidate == input || !strings.HasPrefix(candidate, input) {
		return fmt.Errorf("suggestion must be a strict extension of input")
	}
	if len(candidate) > maxSuggestionBytes || !utf8.ValidString(candidate) {
		return fmt.Errorf("suggestion is invalid or oversized")
	}
	if strings.ContainsAny(candidate, "\r\n") || strings.IndexFunc(candidate, unicode.IsControl) >= 0 {
		return fmt.Errorf("suggestion contains controls or newlines")
	}
	return nil
}

func parentRequestID(params json.RawMessage) (int64, error) {
	var envelope struct {
		ParentRequestID int64 `json:"parent_request_id"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return 0, err
	}
	if envelope.ParentRequestID <= 0 {
		return 0, fmt.Errorf("parent_request_id is required")
	}
	return envelope.ParentRequestID, nil
}
