package shell

import (
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/config"
)

func TestCollectStatus_ReportsConfigError(t *testing.T) {
	cfg := config.Default()
	cfg.LoadIssue = &config.LoadError{
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
