package plugin

import "testing"

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
