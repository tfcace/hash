package plugin

import "testing"

func TestValidateCommandCorrectionAllowsOneStaticTokenAndPreservesLayout(t *testing.T) {
	for _, tc := range []struct{ original, candidate string }{
		{"git sttaus", "git status"},
		{"FOO=bar  git sttaus >out", "FOO=bar  git status >out"},
		{"gti status", "git status"},
		{"tool --verison x", "tool --version x"},
		{"git 'sttaus'", "git 'status'"},
	} {
		if err := ValidateCommandCorrection(tc.original, tc.candidate, "bash"); err != nil {
			t.Errorf("%q -> %q: %v", tc.original, tc.candidate, err)
		}
	}
}

func TestValidateCommandCorrectionRejectsUnsafeStructure(t *testing.T) {
	for _, tc := range []struct{ original, candidate string }{
		{"git sttaus", "git status; rm -rf x"},
		{"git sttaus | cat", "git status | cat"},
		{"echo $(gti)", "echo $(git)"},
		{"git sttaus", "git status now"},
		{"git sttaus >out", "git status >other"},
		{"sudo gti status", "sudo git status"},
		{"eval gti status", "eval git status"},
		{"tool -hlep", "tool -help"},
		{"git foo foo", "git bar foo"},
	} {
		if err := ValidateCommandCorrection(tc.original, tc.candidate, "bash"); err == nil {
			t.Errorf("accepted unsafe correction %q -> %q", tc.original, tc.candidate)
		}
	}
}
