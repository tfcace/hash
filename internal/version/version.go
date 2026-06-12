package version

import "runtime/debug"

var (
	Version   = "dev"
	GitCommit = "unknown"
	JjChange  = "unknown"
	BuildDate = "unknown"
)

// buildString formats the version string, falling back to Go build info
// for values not injected via ldflags (e.g. go install or plain go build).
func buildString(version, gitCommit, jjChange, buildDate string, info *debug.BuildInfo) string {
	version, gitCommit, buildDate = applyBuildInfo(version, gitCommit, buildDate, info)

	v := version
	if suffix := versionSuffix(gitCommit, jjChange, buildDate); suffix != "" {
		v += " (" + suffix + ")"
	}
	return v
}

// applyBuildInfo backfills values not injected via ldflags from Go build info.
func applyBuildInfo(version, gitCommit, buildDate string, info *debug.BuildInfo) (v, commit, date string) {
	if info == nil {
		return version, gitCommit, buildDate
	}
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if !known(gitCommit) {
				gitCommit = s.Value
				if len(gitCommit) > 12 {
					gitCommit = gitCommit[:12]
				}
			}
		case "vcs.time":
			if !known(buildDate) {
				buildDate = s.Value
			}
		}
	}
	return version, gitCommit, buildDate
}

// versionSuffix renders the parenthesized vcs/build-date detail, or "".
func versionSuffix(gitCommit, jjChange, buildDate string) string {
	var parts []string
	if known(jjChange) {
		parts = append(parts, "jj:"+jjChange)
	}
	if known(gitCommit) {
		parts = append(parts, "git:"+gitCommit)
	}
	if known(buildDate) {
		parts = append(parts, buildDate)
	}
	return joinStrings(parts, " ")
}

// known reports whether a build value was actually populated.
func known(s string) bool {
	return s != "unknown" && s != ""
}

func String() string {
	info, _ := debug.ReadBuildInfo()
	return buildString(Version, GitCommit, JjChange, BuildDate, info)
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
