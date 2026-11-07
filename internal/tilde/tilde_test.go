package tilde

import (
	"os/user"
	"path/filepath"
	"testing"
)

func TestExpand(t *testing.T) {
	t.Setenv("HOME", "/h")
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	for in, want := range map[string]string{
		"~":                       "/h",
		"~/.profile":              "/h/.profile",
		"/etc/profile":            "/etc/profile",
		"a/~/b":                   "a/~/b",
		"~hash_no_such_user/rc":   "~hash_no_such_user/rc",
		"~" + me.Username + "/rc": filepath.Join(me.HomeDir, "rc"),
	} {
		if got := Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

// Only the home directory and paths inside it, by whole components, get ~:
// /h2 is not under /h.
func TestAbbrev(t *testing.T) {
	t.Setenv("HOME", "/h/")
	for in, want := range map[string]string{
		"/h":       "~",
		"/h/x/y":   "~/x/y",
		"/h2/x":    "/h2/x",
		"/etc/h/x": "/etc/h/x",
	} {
		if got := Abbrev(in); got != want {
			t.Errorf("Abbrev(%q) = %q, want %q", in, got, want)
		}
	}
	t.Setenv("HOME", "")
	if got := Abbrev("/h/x"); got != "/h/x" {
		t.Errorf("no HOME: Abbrev = %q", got)
	}
}
