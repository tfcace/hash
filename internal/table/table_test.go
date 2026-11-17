package table

import (
	"regexp"
	"strings"
	"testing"
)

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return sgr.ReplaceAllString(s, "") }

var plugins = [][]string{
	{"Command", "Spec", "Rules"},
	{"docker", "docker", "12"},
	{"kubectl", "k8s", "3"},
}

func TestRender_DrawsAlignedColumnsUnderABoldHeaderAndARule(t *testing.T) {
	got := Render(plugins, []Align{Left, Left, Right}, 80)
	want := "Command  Spec    Rules\n" +
		"───────  ──────  ─────\n" +
		"docker   docker     12\n" +
		"kubectl  k8s         3"
	if plain(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", plain(got), want)
	}
	if !strings.HasPrefix(got, bold+"Command"+reset) {
		t.Errorf("the header is not bold: %q", got)
	}
	if !strings.Contains(got, gray+"───────  ──────  ─────"+reset) {
		t.Errorf("the rule is not gray: %q", got)
	}
}

func TestRender_ColumnsWithoutAnAlignmentAreLeftAligned(t *testing.T) {
	if got, want := plain(Render(plugins, nil, 80)), "docker   docker  12"; !strings.Contains(got, want) {
		t.Errorf("no %q in:\n%s", want, got)
	}
}

func TestRender_CentersAColumn(t *testing.T) {
	got := plain(Render([][]string{{"Name"}, {"ab"}}, []Align{Center}, 80))
	if want := "Name\n────\n ab"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_MeasuresCellsWithoutTheirStyling(t *testing.T) {
	got := plain(Render([][]string{{"A", "B"}, {"\x1b[31mred\x1b[0m", "x"}}, nil, 80))
	if want := "A    B\n───  ─\nred  x"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_WrapsACellTooWideForItsColumn(t *testing.T) {
	rows := [][]string{{"Name", "Note"}, {"api", "restarted after the cache flag flipped at noon"}}
	got := plain(Render(rows, nil, 30))
	for _, line := range strings.Split(got, "\n") {
		if n := len([]rune(line)); n > 30 {
			t.Errorf("row %q is %d columns", line, n)
		}
	}
	if !strings.Contains(got, "api") || !strings.Contains(got, "noon") {
		t.Errorf("content lost:\n%s", got)
	}
}

// A table that cannot fit even with its columns squeezed is drawn as one
// record a row.
func TestRender_FallsBackToRecordsWhenThePaneIsTooNarrow(t *testing.T) {
	rows := [][]string{{"Command", "Spec", "Origin", "Rules"}, {"docker", "docker", "built-in", "12"}}
	got := plain(Render(rows, nil, 20))
	if want := "Command: docker\nSpec: docker\nOrigin: built-in\nRules: 12"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_AWidthBelowOneIsNoLimit(t *testing.T) {
	long := strings.Repeat("x", 300)
	if got := plain(Render([][]string{{"A"}, {long}}, nil, 0)); !strings.Contains(got, long) {
		t.Errorf("the cell was wrapped with no limit set")
	}
}

func TestRender_NothingToDrawIsEmpty(t *testing.T) {
	if got := Render(nil, nil, 80); got != "" {
		t.Errorf("got %q", got)
	}
	if got := Render([][]string{{}}, nil, 80); got != "" {
		t.Errorf("got %q", got)
	}
}
