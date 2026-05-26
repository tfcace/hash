package completion

import (
	"testing"
)

func TestMakeHandler_Targets(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"build: src/*.go",
				"test: build",
				"clean:",
				".PHONY: build test clean",
			}, nil
		},
	}

	targets := h.parseFile("")
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d: %v", len(targets), targets)
	}
}

func TestMakeHandler_SkipsSpecialTargets(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"build:",
				".DEFAULT_GOAL:",
				".PHONY: build",
				".SUFFIXES:",
			}, nil
		},
	}

	targets := h.parseFile("")
	if len(targets) != 1 {
		t.Fatalf("expected 1 target (special skipped), got %d: %v", len(targets), targets)
	}
	if targets[0] != "build" {
		t.Errorf("expected build, got %q", targets[0])
	}
}

func TestMakeHandler_PrefixFilter(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"build:",
				"build-docker:",
				"test:",
			}, nil
		},
	}

	// Since parseTargets looks for files in cwd, we test parseFile directly
	targets := h.parseFile("")
	filtered := prefixFilterItems(targets, "build")
	if len(filtered.Items) != 2 {
		t.Fatalf("expected 2 items matching 'build', got %d", len(filtered.Items))
	}
}

func TestMakeHandler_NoMatch(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{"build:", "test:"}, nil
		},
	}

	targets := h.parseFile("")
	filtered := prefixFilterItems(targets, "deploy")
	if len(filtered.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(filtered.Items))
	}
}

func TestMakeHandler_EmptyFile(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return nil, nil
		},
	}

	targets := h.parseFile("")
	if len(targets) != 0 {
		t.Fatalf("expected 0 targets, got %d", len(targets))
	}
}

func TestMakeHandler_Deduplicates(t *testing.T) {
	h := &MakeHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"build: src/a.go",
				"build: src/b.go", // duplicate target
				"test:",
			}, nil
		},
	}

	targets := h.parseFile("")
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets (deduped), got %d: %v", len(targets), targets)
	}
}
