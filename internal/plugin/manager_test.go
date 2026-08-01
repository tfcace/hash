package plugin

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestManagerRestartsOnceAfterUnexpectedExit(t *testing.T) {
	if os.Getenv("HASH_PLUGIN_TEST_HELPER") != "" {
		return
	}
	t.Setenv("HASH_PLUGIN_TEST_HELPER", "crash-once")
	t.Setenv("HASH_PLUGIN_CRASH_STATE", filepath.Join(t.TempDir(), "state"))
	bundle := t.TempDir()
	if err := os.Symlink(os.Args[0], filepath.Join(bundle, "helper")); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ManifestVersion: 1, ID: "io.runhash.restart", Name: "Restart", Version: "0.1.0", ProtocolVersion: 1, Entrypoint: "helper", Directory: bundle, Hooks: []string{"command.finished"}}
	manager, err := NewManager([]Manifest{manifest}, []string{manifest.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	var result CommandFinishedResult
	if found, err := manager.CallFirst(context.Background(), "command.finished", CommandFinishedParams{ExecutedLine: "git sttaus", ExitCode: 1}, &result); err != nil || found {
		t.Fatalf("first call found=%v err=%v", found, err)
	}
	if found, err := manager.CallFirst(context.Background(), "command.finished", CommandFinishedParams{ExecutedLine: "git sttaus", ExitCode: 1}, &result); err != nil || !found {
		t.Fatalf("second call found=%v err=%v", found, err)
	}
	if len(result.Corrections) != 1 || result.Corrections[0] != "git status" {
		t.Fatalf("result=%+v", result)
	}
}

func TestManagerUsesHealthyLowerPriorityResultAtDeadline(t *testing.T) {
	if os.Getenv("HASH_PLUGIN_TEST_HELPER") != "" {
		return
	}
	t.Setenv("HASH_PLUGIN_TEST_HELPER", "priority")
	bundle := t.TempDir()
	if err := os.Symlink(os.Args[0], filepath.Join(bundle, "helper")); err != nil {
		t.Fatal(err)
	}
	makeManifest := func(id string) Manifest {
		return Manifest{ManifestVersion: 1, ID: id, Name: id, Version: "0.1.0", ProtocolVersion: 1, Entrypoint: "helper", Directory: bundle, Hooks: []string{"command.finished"}}
	}
	slow := makeManifest("io.runhash.slow")
	fast := makeManifest("io.runhash.fast")
	manager, err := NewManager([]Manifest{slow, fast}, []string{slow.ID, fast.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		var result CommandFinishedResult
		found, err := manager.CallFirst(ctx, "command.finished", CommandFinishedParams{ExecutedLine: "git sttaus", ExitCode: 1}, &result)
		cancel()
		if err != nil || !found || len(result.Corrections) != 1 {
			t.Fatalf("attempt %d found=%v err=%v result=%+v", attempt, found, err, result)
		}
	}
	manager.mu.RLock()
	disabled := manager.enabled[0].disabled
	manager.mu.RUnlock()
	if !disabled {
		t.Fatal("slow plugin was not disabled after three deadlines")
	}
}

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
