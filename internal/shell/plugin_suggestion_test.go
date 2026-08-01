package shell

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tfcace/hash/internal/plugin"
)

type stubEditorSuggestionCaller struct {
	results        []json.RawMessage
	beforeValidate func()
	afterValidate  func()
	method         string
	params         plugin.EditorSuggestParams
	validated      int
}

func (s *stubEditorSuggestionCaller) CallFirstValid(
	_ context.Context,
	method string,
	params any,
	validate func(json.RawMessage) bool,
	_ any,
) (bool, error) {
	s.method = method
	s.params = params.(plugin.EditorSuggestParams)
	if s.beforeValidate != nil {
		s.beforeValidate()
	}
	for _, result := range s.results {
		s.validated++
		if validate(result) {
			if s.afterValidate != nil {
				s.afterValidate()
			}
			return true, nil
		}
	}
	return false, nil
}

func TestCallEditorSuggestionFallsThroughEmptyProvider(t *testing.T) {
	caller := &stubEditorSuggestionCaller{results: []json.RawMessage{
		json.RawMessage(`{"text":""}`),
		json.RawMessage(`{"text":"git status"}`),
	}}
	params := plugin.EditorSuggestParams{Generation: 7, Trigger: "edit", Line: "git", Cursor: 3, Dialect: "bash"}

	got := callEditorSuggestion(context.Background(), caller, params, func() uint64 { return 7 })

	if got != "git status" {
		t.Fatalf("callEditorSuggestion() = %q, want %q", got, "git status")
	}
	if caller.method != "editor.suggest" || caller.params != params {
		t.Fatalf("request = (%q, %+v), want editor.suggest with %+v", caller.method, caller.params, params)
	}
	if caller.validated != 2 {
		t.Fatalf("validated %d providers, want 2", caller.validated)
	}
}

func TestCallEditorSuggestionFallsThroughInvalidProvider(t *testing.T) {
	caller := &stubEditorSuggestionCaller{results: []json.RawMessage{
		json.RawMessage(`{"text":"git 'unterminated"}`),
		json.RawMessage(`{"text":"git status"}`),
	}}
	params := plugin.EditorSuggestParams{Generation: 9, Trigger: "edit", Line: "git", Cursor: 3, Dialect: "bash"}

	if got := callEditorSuggestion(context.Background(), caller, params, func() uint64 { return 9 }); got != "git status" {
		t.Fatalf("callEditorSuggestion() = %q, want lower-priority valid result", got)
	}
	if caller.validated != 2 {
		t.Fatalf("validated %d providers, want 2", caller.validated)
	}
}

func TestCallEditorSuggestionRejectsAllInvalidProviders(t *testing.T) {
	invalidUTF8 := json.RawMessage([]byte("{\"text\":\"git \xff\"}"))
	caller := &stubEditorSuggestionCaller{results: []json.RawMessage{
		json.RawMessage(`{"text":`),
		json.RawMessage(`{"text":"git"}`),
		json.RawMessage(`{"text":"docker ps"}`),
		invalidUTF8,
	}}
	params := plugin.EditorSuggestParams{Generation: 11, Trigger: "edit", Line: "git", Cursor: 3, Dialect: "bash"}

	if got := callEditorSuggestion(context.Background(), caller, params, func() uint64 { return 11 }); got != "" {
		t.Fatalf("callEditorSuggestion() = %q, want no suggestion", got)
	}
	if caller.validated != len(caller.results) {
		t.Fatalf("validated %d providers, want %d", caller.validated, len(caller.results))
	}
}

func TestCallEditorSuggestionRejectsStaleGeneration(t *testing.T) {
	generation := uint64(13)
	caller := &stubEditorSuggestionCaller{
		results: []json.RawMessage{json.RawMessage(`{"text":"git status"}`)},
		beforeValidate: func() {
			generation++
		},
	}
	params := plugin.EditorSuggestParams{Generation: generation, Trigger: "edit", Line: "git", Cursor: 3, Dialect: "bash"}

	if got := callEditorSuggestion(context.Background(), caller, params, func() uint64 { return generation }); got != "" {
		t.Fatalf("callEditorSuggestion() = %q, want stale response rejected", got)
	}
	if caller.validated != 1 {
		t.Fatalf("validated %d providers, want 1", caller.validated)
	}
}

func TestCallEditorSuggestionRejectsGenerationThatChangesAfterSelection(t *testing.T) {
	generation := uint64(15)
	caller := &stubEditorSuggestionCaller{
		results: []json.RawMessage{json.RawMessage(`{"text":"git status"}`)},
		afterValidate: func() {
			generation++
		},
	}
	params := plugin.EditorSuggestParams{Generation: generation, Trigger: "edit", Line: "git", Cursor: 3, Dialect: "bash"}

	if got := callEditorSuggestion(context.Background(), caller, params, func() uint64 { return generation }); got != "" {
		t.Fatalf("callEditorSuggestion() = %q, want response rejected after generation changed", got)
	}
}
