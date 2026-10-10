package agentupdate

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.42.0", "0.88.0", -1},
		{"0.88.0", "0.42.0", 1},
		{"0.88.0", "0.88.0", 0},
		{"1.0.0", "0.99.9", 1},
		{"0.88.1-preview.1", "0.88.1", -1},
		{"0.88.1", "0.88.1-preview.1", 1},
		{"0.88.1-preview.1", "0.88.1-preview.2", -1},
		{"v0.88.0", "0.88.0", 0},
		{"0.88", "0.88.0", 0},
		{"0.88.0+build.5", "0.88.0", 0},
		{"", "0.1.0", -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
