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
)

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
		if strings.ContainsAny(candidate, "\r\n") || strings.IndexFunc(candidate, func(r rune) bool {
			return unicode.IsControl(r)
		}) >= 0 {
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
