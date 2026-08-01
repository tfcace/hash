package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/plugin"
)

func runPlugin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPluginHelp(stdout)
		return 2
	}
	manifests, discoveryErr := plugin.Discover(plugin.PluginRoots())
	if discoveryErr != nil {
		fmt.Fprintf(stderr, "hash: plugin discovery: %v\n", discoveryErr)
		return 1
	}
	switch args[0] {
	case "list":
		cfg, _ := config.Load(getConfigDir())
		enabled := map[string]bool{}
		if cfg != nil {
			for _, id := range cfg.Plugins.Enabled {
				enabled[id] = true
			}
		}
		if len(manifests) == 0 {
			fmt.Fprintln(stdout, "No Hash plugins installed.")
			return 0
		}
		for _, manifest := range manifests {
			state := "disabled"
			if enabled[manifest.ID] {
				state = "enabled"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", manifest.ID, manifest.Version, state, manifest.Name)
		}
		return 0
	case "inspect":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "Usage: hash plugin inspect <id>")
			return 2
		}
		manifest, ok := findPlugin(manifests, args[1])
		if !ok {
			fmt.Fprintf(stderr, "hash: plugin %q is not installed\n", args[1])
			return 1
		}
		fmt.Fprintf(stdout, "%s (%s)\nversion: %s\nentrypoint: %s\nhooks: %s\ncommands: %s\ncontext: %s\nhost services: %s\n\nSecurity: this trusted executable receives your OS user privileges. Capabilities are informational metadata, not a sandbox.\n",
			manifest.Name, manifest.ID, manifest.Version, manifest.Executable(), listOrNone(manifest.Hooks), listOrNone(manifest.Commands), listOrNone(manifest.Capabilities.Context), listOrNone(manifest.Capabilities.HostServices))
		return 0
	case "enable", "disable":
		if len(args) != 2 {
			fmt.Fprintf(stderr, "Usage: hash plugin %s <id>\n", args[0])
			return 2
		}
		if _, ok := findPlugin(manifests, args[1]); !ok {
			fmt.Fprintf(stderr, "hash: plugin %q is not installed\n", args[1])
			return 1
		}
		enable := args[0] == "enable"
		if err := config.SetPluginEnabled(getConfigDir(), args[1], enable); err != nil {
			fmt.Fprintf(stderr, "hash: update plugin configuration: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s %s\n", map[bool]string{true: "Enabled", false: "Disabled"}[enable], args[1])
		return 0
	case "link":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "Usage: hash plugin link <bundle-directory>")
			return 2
		}
		return linkPlugin(args[1], stdout, stderr)
	case "doctor":
		if len(args) > 2 {
			fmt.Fprintln(stderr, "Usage: hash plugin doctor [id]")
			return 2
		}
		id := ""
		if len(args) == 2 {
			id = args[1]
		}
		return doctorPlugins(manifests, id, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "hash: unknown plugin command %q\n", args[0])
		printPluginHelp(stderr)
		return 2
	}
}

func linkPlugin(source string, stdout, stderr io.Writer) int {
	absSource, err := filepath.Abs(source)
	if err != nil {
		fmt.Fprintf(stderr, "hash: resolve plugin bundle: %v\n", err)
		return 1
	}
	manifest, err := plugin.LoadManifest(absSource)
	if err != nil {
		fmt.Fprintf(stderr, "hash: invalid plugin bundle: %v\n", err)
		return 1
	}
	roots := plugin.PluginRoots()
	if len(roots) == 0 {
		fmt.Fprintln(stderr, "hash: no XDG user data directory is available")
		return 1
	}
	target := filepath.Join(roots[0], manifest.ID)
	if _, err := os.Lstat(target); err == nil {
		fmt.Fprintf(stderr, "hash: plugin target already exists: %s\n", target)
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "hash: inspect plugin target: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(stderr, "hash: create plugin directory: %v\n", err)
		return 1
	}
	if err := os.Symlink(absSource, target); err != nil {
		fmt.Fprintf(stderr, "hash: link plugin: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Linked %s at %s\n", manifest.ID, target)
	return 0
}

func doctorPlugins(manifests []plugin.Manifest, selectedID string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(getConfigDir())
	if err != nil {
		fmt.Fprintf(stderr, "hash: configuration: %v\n", err)
		return 1
	}
	installed := make(map[string]plugin.Manifest, len(manifests))
	for _, manifest := range manifests {
		installed[manifest.ID] = manifest
	}
	failed := false
	ids := append([]string(nil), cfg.Plugins.Enabled...)
	if selectedID != "" {
		ids = []string{selectedID}
	}
	for _, id := range ids {
		manifest, ok := installed[id]
		if !ok {
			fmt.Fprintf(stdout, "FAIL %s: enabled but not installed\n", id)
			failed = true
			continue
		}
		if info, err := os.Stat(manifest.Executable()); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			fmt.Fprintf(stdout, "FAIL %s: entrypoint is not executable: %s\n", id, manifest.Executable())
			failed = true
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		client, err := plugin.StartProcess(ctx, manifest, cfg.Plugins.Settings[id])
		cancel()
		if err != nil {
			fmt.Fprintf(stdout, "FAIL %s: handshake failed: %v\n", id, err)
			failed = true
			continue
		}
		if err := client.Close(); err != nil {
			fmt.Fprintf(stdout, "FAIL %s: shutdown failed: %v\n", id, err)
			failed = true
			continue
		}
		fmt.Fprintf(stdout, "OK   %s: executable, protocol version, handshake and shutdown passed\n", id)
	}
	if len(ids) == 0 {
		fmt.Fprintln(stdout, "OK   no plugins enabled (fresh-install default)")
	}
	if failed {
		return 1
	}
	return 0
}

func findPlugin(manifests []plugin.Manifest, id string) (plugin.Manifest, bool) {
	for _, manifest := range manifests {
		if manifest.ID == id {
			return manifest, true
		}
	}
	return plugin.Manifest{}, false
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, ", ")
}

func printPluginHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: hash plugin <list|inspect|enable|disable|link|doctor> [arguments]")
}
