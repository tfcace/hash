package version

var (
	Version   = "dev"
	GitCommit = "unknown"
	JjChange  = "unknown"
	BuildDate = "unknown"
)

func String() string {
	v := Version

	var vcsInfo []string
	if JjChange != "unknown" && JjChange != "" {
		vcsInfo = append(vcsInfo, "jj:"+JjChange)
	}
	if GitCommit != "unknown" && GitCommit != "" {
		vcsInfo = append(vcsInfo, "git:"+GitCommit)
	}

	if len(vcsInfo) > 0 || (BuildDate != "unknown" && BuildDate != "") {
		v += " ("
		if len(vcsInfo) > 0 {
			v += joinStrings(vcsInfo, " ")
		}
		if BuildDate != "unknown" && BuildDate != "" {
			if len(vcsInfo) > 0 {
				v += " "
			}
			v += BuildDate
		}
		v += ")"
	}

	return v
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
