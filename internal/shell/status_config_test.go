package shell

import (
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/config"
)

func TestCollectStatus_ReportsConfigError(t *testing.T) {
	cfg := config.Default()
	cfg.LoadIssue = &config.LoadError{
		Kind:        config.LoadPartial,
		Path:        "/tmp/config.toml",
		BadSections: []string{"shell"},
		Detail:      "line 3: cannot decode",
	}
	s := &Shell{config: cfg}

	status := s.collectStatus()
	if status.ConfigOK {
		t.Error("ConfigOK should be false when the config had load errors")
	}
	if !strings.Contains(status.ConfigErr, "shell") {
		t.Errorf("ConfigErr = %q, should name the reverted section", status.ConfigErr)
	}

	out := status.Format()
	if !strings.Contains(out, "Config:") {
		t.Error("status output should include a Config line")
	}
}

func TestCollectStatus_ConfigOKByDefault(t *testing.T) {
	s := &Shell{config: config.Default()}

	status := s.collectStatus()
	if !status.ConfigOK {
		t.Error("ConfigOK should be true without load errors")
	}
	if out := status.Format(); !strings.Contains(out, "Config:") {
		t.Error("status output should include a Config line")
	}
}

// A config that could not be read is not a parse error: status must say the
// file could not be read, so the user checks permissions, not syntax.
func TestCollectStatus_NamesAnUnreadableConfig(t *testing.T) {
	cfg := config.Default()
	cfg.LoadIssue = &config.LoadError{
		Kind:   config.LoadReadFailed,
		Path:   "/tmp/config.toml",
		Detail: "permission denied",
	}
	s := &Shell{config: cfg}

	status := s.collectStatus()
	if strings.Contains(status.ConfigErr, "parse") || !strings.Contains(status.ConfigErr, "could not be read") {
		t.Fatalf("ConfigErr = %q, want it to say the file could not be read", status.ConfigErr)
	}
}

// A syntax error means every setting is on defaults.
func TestCollectStatus_NamesASyntaxError(t *testing.T) {
	cfg := config.Default()
	cfg.LoadIssue = &config.LoadError{Kind: config.LoadSyntaxError, Path: "/tmp/config.toml", Detail: "line 1"}
	status := (&Shell{config: cfg}).collectStatus()
	if !strings.Contains(status.ConfigErr, "parse error") {
		t.Errorf("ConfigErr = %q, want a parse error", status.ConfigErr)
	}
}
