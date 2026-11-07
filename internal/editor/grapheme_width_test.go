package editor

import (
	"testing"
	"unicode/utf8"
)

const family = "\U0001F468\u200D\U0001F469\u200D\U0001F467" // one cluster, two cells

// Vertical motion finds columns with the measure the display draws with.
// It added rune widths (6 cells for the ZWJ family, which draws in 2), so
// Up and Down landed inside the cluster or on the wrong column.
func TestByteColForAbs_MatchesDisplayWidth(t *testing.T) {
	layout := fixedLayout{40, 2, 1}
	for _, line := range []string{family + "x", "a" + family + "b" + family, "éx", "\U0001F1EE\U0001F1F1y"} {
		boundaries := []int{0}
		for col := 0; col < len(line); {
			col = nextCluster(line, col)
			boundaries = append(boundaries, col)
		}
		for _, col := range boundaries {
			target := layout.PrefixWidth(0) + visibleWidthAtByteIndex(line, col)
			if got := byteColForAbs(layout, line, 0, target); got != col {
				t.Errorf("%q: column at cell %d = byte %d, the display draws byte %d there", line, target, got, col)
			}
		}
	}
}

// nextCluster is the byte after the grapheme cluster at col (test helper,
// independent of the code under test).
func nextCluster(s string, col int) int {
	// Clusters in the cases above: a base rune plus ZWJ sequences,
	// combining marks, or a regional-indicator pair.
	i := col
	r, size := utf8.DecodeRuneInString(s[i:])
	i += size
	if r >= 0x1F1E6 && r <= 0x1F1FF && i < len(s) {
		_, size = utf8.DecodeRuneInString(s[i:])
		return i + size
	}
	for i < len(s) {
		r, size = utf8.DecodeRuneInString(s[i:])
		switch r {
		case 0x200D:
			_, next := utf8.DecodeRuneInString(s[i+size:])
			i += size + next
		case 0x0301:
			i += size
		default:
			return i
		}
	}
	return i
}

func TestTruncateToWidth_KeepsClustersWhole(t *testing.T) {
	s := family + family + family + "abc"
	got := truncateToWidth(s, 7)
	if w := visibleWidth(got); w > 7 {
		t.Errorf("truncateToWidth(%q, 7) = %q, %d cells", s, got, w)
	}
	if got != family+family+"..." {
		t.Errorf("truncateToWidth = %q, want two whole families and the dots", got)
	}
}
