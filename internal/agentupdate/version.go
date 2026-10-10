package agentupdate

import (
	"strconv"
	"strings"
)

// Compare orders two npm-style versions: numeric major.minor.patch, with a
// prerelease (anything after "-") sorting below its release. Returns -1, 0
// or 1. Missing numeric parts count as 0; a leading "v" is ignored.
// Prerelease identifiers compare as plain strings and build metadata is
// ignored: enough to answer whether a newer release is out, not a full
// semver implementation.
func Compare(a, b string) int {
	an, apre := splitVersion(a)
	bn, bpre := splitVersion(b)
	for i := 0; i < 3; i++ {
		if an[i] != bn[i] {
			if an[i] < bn[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	case apre < bpre:
		return -1
	default:
		return 1
	}
}

func splitVersion(v string) (nums [3]int, pre string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	for i, part := range strings.SplitN(v, ".", 3) {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		nums[i] = n
	}
	return nums, pre
}
