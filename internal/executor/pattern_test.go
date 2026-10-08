package executor

import (
	"regexp"
	"testing"

	"mvdan.cc/sh/v3/pattern"
)

// TestShellPattern_NegatedPOSIXClassCompiles guards the NVM alias pattern
// that panicked the shell on mvdan/sh 3.13.1 (see mvdan/sh#1374).
func TestShellPattern_NegatedPOSIXClassCompiles(t *testing.T) {
	expr, err := pattern.Regexp(`*[![:space:]]*`, pattern.EntireString)
	if err != nil {
		t.Fatalf("NVM shell pattern failed: %v", err)
	}
	rx, err := regexp.Compile(expr)
	if err != nil {
		t.Fatalf("NVM shell pattern produced invalid regexp %q: %v", expr, err)
	}
	if !rx.MatchString("lts/krypton") {
		t.Fatal("NVM shell pattern should match a non-space alias")
	}
	if rx.MatchString(" \t") {
		t.Fatal("NVM shell pattern should not match an all-space alias")
	}
}
