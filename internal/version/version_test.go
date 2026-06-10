package version

import (
	"runtime/debug"
	"testing"
)

func testBuildInfo(mainVersion string, settings map[string]string) *debug.BuildInfo {
	info := &debug.BuildInfo{}
	info.Main.Version = mainVersion
	for k, v := range settings {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return info
}

func TestBuildStringLdflagsTakePrecedence(t *testing.T) {
	info := testBuildInfo("v9.9.9", map[string]string{
		"vcs.revision": "ffffffffffffffffffffffffffffffffffffffff",
		"vcs.time":     "1999-01-01T00:00:00Z",
	})
	got := buildString("v0.6.0", "abc1234", "xyzkmnop", "2026-06-01", info)
	want := "v0.6.0 (jj:xyzkmnop git:abc1234 2026-06-01)"
	if got != want {
		t.Errorf("buildString() = %q, want %q", got, want)
	}
}

func TestBuildStringGoInstallPseudoVersion(t *testing.T) {
	info := testBuildInfo("v0.6.1-0.20260610120000-abc123def456", nil)
	got := buildString("dev", "unknown", "unknown", "unknown", info)
	want := "v0.6.1-0.20260610120000-abc123def456"
	if got != want {
		t.Errorf("buildString() = %q, want %q", got, want)
	}
}

func TestBuildStringLocalBuildVCSStamping(t *testing.T) {
	info := testBuildInfo("(devel)", map[string]string{
		"vcs.revision": "abc123def4567890abc123def4567890abc123de",
		"vcs.time":     "2026-06-10T12:00:00Z",
	})
	got := buildString("dev", "unknown", "unknown", "unknown", info)
	want := "dev (git:abc123def456 2026-06-10T12:00:00Z)"
	if got != want {
		t.Errorf("buildString() = %q, want %q", got, want)
	}
}

func TestBuildStringShortRevisionNotTruncated(t *testing.T) {
	info := testBuildInfo("(devel)", map[string]string{
		"vcs.revision": "abc123",
	})
	got := buildString("dev", "unknown", "unknown", "unknown", info)
	want := "dev (git:abc123)"
	if got != want {
		t.Errorf("buildString() = %q, want %q", got, want)
	}
}

func TestBuildStringNoBuildInfo(t *testing.T) {
	got := buildString("dev", "unknown", "unknown", "unknown", nil)
	want := "dev"
	if got != want {
		t.Errorf("buildString() = %q, want %q", got, want)
	}
}

func TestBuildStringEmptyMainVersionStaysDev(t *testing.T) {
	info := testBuildInfo("", nil)
	got := buildString("dev", "unknown", "unknown", "unknown", info)
	want := "dev"
	if got != want {
		t.Errorf("buildString() = %q, want %q", got, want)
	}
}
