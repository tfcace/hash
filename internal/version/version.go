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
	if info != nil {
		if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if gitCommit == "unknown" || gitCommit == "" {
					gitCommit = s.Value
					if len(gitCommit) > 12 {
						gitCommit = gitCommit[:12]
					}
				}
			case "vcs.time":
				if buildDate == "unknown" || buildDate == "" {
					buildDate = s.Value
				}
			}
		}
	}

	v := version

	var vcsInfo []string
	if jjChange != "unknown" && jjChange != "" {
		vcsInfo = append(vcsInfo, "jj:"+jjChange)
	}
	if gitCommit != "unknown" && gitCommit != "" {
		vcsInfo = append(vcsInfo, "git:"+gitCommit)
	}

	if len(vcsInfo) > 0 || (buildDate != "unknown" && buildDate != "") {
		v += " ("
		if len(vcsInfo) > 0 {
			v += joinStrings(vcsInfo, " ")
		}
		if buildDate != "unknown" && buildDate != "" {
			if len(vcsInfo) > 0 {
				v += " "
			}
			v += buildDate
		}
		v += ")"
	}

	return v
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
