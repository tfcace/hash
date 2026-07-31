package plugin

import (
	"reflect"
	"testing"
)

func TestPluginRootsUsesXDGDataLocations(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/user-data")
	t.Setenv("XDG_DATA_DIRS", "/system-one:/system-two")
	got := PluginRoots()
	want := []string{
		"/user-data/hash/plugins",
		"/system-one/hash/plugins",
		"/system-two/hash/plugins",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginRoots() = %q, want %q", got, want)
	}
}

func TestNewManagerUsesEnabledOrderAndRejectsUnknownAndDuplicateIDs(t *testing.T) {
	manifests := []Manifest{
		{ID: "io.runhash.second"},
		{ID: "io.runhash.first"},
	}
	manager, err := NewManager(manifests, []string{"io.runhash.first", "io.runhash.second"}, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if got, want := manager.EnabledIDs(), []string{"io.runhash.first", "io.runhash.second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EnabledIDs() = %q, want %q", got, want)
	}
	if _, err := NewManager(manifests, []string{"missing"}, nil); err == nil {
		t.Fatal("NewManager() error = nil, want unknown plugin error")
	}
	if _, err := NewManager(manifests, []string{"io.runhash.first", "io.runhash.first"}, nil); err == nil {
		t.Fatal("NewManager() error = nil, want duplicate enabled ID error")
	}
}
