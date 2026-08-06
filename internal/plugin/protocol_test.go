package plugin

import "testing"

func TestValidateSuggestionCandidateRejectsUnsafeOrNonPrefixText(t *testing.T) {
	for _, candidate := range []string{"git\nstatus", "git\x00status", "git status"} {
		if err := ValidateSuggestionCandidate("git st", candidate); err != nil && candidate == "git status" {
			t.Fatalf("safe prefix suggestion rejected: %v", err)
		}
	}
	if err := ValidateSuggestionCandidate("git st", "docker ps"); err == nil {
		t.Fatal("expected non-prefix suggestion to be rejected")
	}
	if err := ValidateSuggestionCandidate("git st", "git\nstatus"); err == nil {
		t.Fatal("expected multiline suggestion to be rejected")
	}
}

func TestValidateCorrectionsRejectsControlsDuplicatesAndOverflow(t *testing.T) {
	candidates := []string{"git status", "git status", "git\tstatus", "git\nstatus", ""}
	got, err := ValidateCorrections("git sttaus", candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "git status" {
		t.Fatalf("got %q", got)
	}
	if _, err := ValidateCorrections("x", []string{"a", "b", "c", "d", "e", "f"}); err == nil {
		t.Fatal("expected too-many-corrections error")
	}
}
