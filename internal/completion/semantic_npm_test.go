package completion

import (
	"context"
	"testing"
)

func TestNPMHandler_RunScripts(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{
				"scripts": {
					"build": "tsc",
					"test": "jest",
					"lint": "eslint ."
				}
			}`), nil
		},
	}

	result := h.Complete(context.Background(), []string{"run"}, "")
	if len(result.Items) != 3 {
		t.Fatalf("expected 3 items, got %d: %+v", len(result.Items), result.Items)
	}
}

func TestNPMHandler_PrefixFilter(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{
				"scripts": {
					"build": "tsc",
					"build:watch": "tsc -w",
					"test": "jest"
				}
			}`), nil
		},
	}

	result := h.Complete(context.Background(), []string{"run"}, "build")
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 items matching 'build', got %d", len(result.Items))
	}
}

func TestNPMHandler_OnlyRunSubcommand(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{"scripts": {"build": "tsc"}}`), nil
		},
	}

	// Should not complete for non-run subcommands
	result := h.Complete(context.Background(), []string{"install"}, "build")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items for 'install', got %d", len(result.Items))
	}
}

func TestNPMHandler_NoPackageJSON(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return nil, &dummyError{msg: "no such file"}
		},
	}

	result := h.Complete(context.Background(), []string{"run"}, "")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items without package.json, got %d", len(result.Items))
	}
}

func TestNPMHandler_EmptyArgs(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{"scripts": {"build": "tsc"}}`), nil
		},
	}

	result := h.Complete(context.Background(), nil, "")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items without subcommand, got %d", len(result.Items))
	}
}

func TestNPMHandler_ScriptDescription(t *testing.T) {
	h := &NPMHandler{
		readFile: func(path string) ([]byte, error) {
			return []byte(`{"scripts": {"build": "webpack --mode production"}}`), nil
		},
	}

	result := h.Complete(context.Background(), []string{"run"}, "")
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	if result.Items[0].Description != "webpack --mode production" {
		t.Errorf("description = %q, want script content", result.Items[0].Description)
	}
}

type dummyError struct {
	msg string
}

func (e *dummyError) Error() string { return e.msg }
