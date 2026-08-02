package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/plugin"
)

type pluginLifecycle interface {
	Install(context.Context, string) (plugin.InstallResult, error)
	Upgrade(context.Context, string, string) (plugin.InstallResult, error)
	Uninstall(string) error
}

type pluginIDInstaller interface {
	InstallForID(context.Context, string, string) (plugin.InstallResult, error)
}

type pluginAllInstaller interface {
	InstallAll(context.Context, string) ([]plugin.InstallResult, error)
}

type pluginAllUpgrader interface {
	UpgradeAll(context.Context, string) (plugin.UpgradeAllResult, error)
}

var installPluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)+$`)

var (
	loadPluginConfig = config.Load
	setPluginEnabled = config.SetPluginEnabled
)

func runPlugin(args []string, stdout, stderr io.Writer) int { //nolint:gocyclo // CLI subcommand dispatch is intentionally explicit
	if len(args) == 0 {
		printPluginHelp(stdout)
		return 2
	}
	if args[0] == "install" || args[0] == "upgrade" || args[0] == "uninstall" {
		lifecycle, err := newPluginLifecycle()
		if err != nil {
			fmt.Fprintf(stderr, "hash: plugin lifecycle: %v\n", err)
			return 1
		}
		return runPluginLifecycle(args, lifecycle, stdout, stderr)
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
		for i := range manifests {
			manifest := &manifests[i]
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

func newPluginLifecycle() (pluginLifecycle, error) {
	pluginRoot := plugin.UserPluginRoot()
	if pluginRoot == "" {
		return nil, fmt.Errorf("no XDG user data directory is available")
	}
	bundleRoot := filepath.Join(filepath.Dir(pluginRoot), "plugin-bundles")
	return plugin.NewInstaller(pluginRoot, bundleRoot), nil
}

func runPluginLifecycle(args []string, lifecycle pluginLifecycle, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch args[0] {
	case "install":
		return runPluginInstall(ctx, args, lifecycle, stdout, stderr)
	case "upgrade":
		return runPluginUpgrade(ctx, args, lifecycle, stdout, stderr)
	case "uninstall":
		return runPluginUninstall(args, lifecycle, stdout, stderr)
	default:
		return 2
	}
}

func runPluginInstall(ctx context.Context, args []string, lifecycle pluginLifecycle, stdout, stderr io.Writer) int {
	source, id, installAll, ok := parseInstallArgs(args)
	if !ok {
		fmt.Fprintln(stderr, "Usage: hash plugin install (--id <plugin-id> | --all)? <github-repository-or-artifact-url>")
		return 2
	}
	if installAll {
		installer, supportsAll := lifecycle.(pluginAllInstaller)
		if !supportsAll {
			fmt.Fprintln(stderr, "hash: this installer does not support installing all plugins")
			return 1
		}
		results, err := installer.InstallAll(ctx, source)
		if err != nil {
			fmt.Fprintf(stderr, "hash: install all plugins: %v\n", err)
			return 1
		}
		if err := disableInstalledPlugins(lifecycle, results); err != nil {
			fmt.Fprintf(stderr, "hash: disable installed plugins: %v\n", err)
			return 1
		}
		for n := range results {
			result := &results[n]
			fmt.Fprintf(stdout, "Installed %s %s\nSource: %s\n", result.ID, result.Version, result.Source)
		}
		fmt.Fprintln(stdout, "Security: these trusted executables receive your OS user privileges. The plugins remain disabled until explicitly enabled.")
		return 0
	}
	var result plugin.InstallResult
	var err error
	if id != "" {
		installer, supportsIDs := lifecycle.(pluginIDInstaller)
		if !supportsIDs {
			fmt.Fprintln(stderr, "hash: this installer does not support selecting a plugin ID")
			return 1
		}
		result, err = installer.InstallForID(ctx, source, id)
	} else {
		result, err = lifecycle.Install(ctx, source)
	}
	if err != nil {
		fmt.Fprintf(stderr, "hash: install plugin: %v\n", err)
		return 1
	}
	if err := disableInstalledPlugins(lifecycle, []plugin.InstallResult{result}); err != nil {
		fmt.Fprintf(stderr, "hash: disable installed plugin: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Installed %s %s\nSource: %s\n", result.ID, result.Version, result.Source)
	fmt.Fprintln(stdout, "Security: this trusted executable receives your OS user privileges. The plugin remains disabled until explicitly enabled.")
	return 0
}

func disableInstalledPlugins(lifecycle pluginLifecycle, results []plugin.InstallResult) error {
	previous, loadErr := loadPluginConfig(getConfigDir())
	if loadErr != nil {
		return rollbackInstalledPlugins(lifecycle, results, loadErr)
	}
	previouslyEnabled := make(map[string]bool, len(previous.Plugins.Enabled))
	for _, id := range previous.Plugins.Enabled {
		previouslyEnabled[id] = true
	}
	disabled := make([]string, 0, len(results))
	for n := range results {
		result := &results[n]
		if err := setPluginEnabled(getConfigDir(), result.ID, false); err != nil {
			var restoreErrors []string
			for n := len(disabled) - 1; n >= 0; n-- {
				id := disabled[n]
				if restoreErr := setPluginEnabled(getConfigDir(), id, previouslyEnabled[id]); restoreErr != nil {
					restoreErrors = append(restoreErrors, fmt.Sprintf("%s: %v", id, restoreErr))
				}
			}
			return rollbackInstalledPlugins(lifecycle, results, err, restoreErrors...)
		}
		disabled = append(disabled, result.ID)
	}
	return nil
}

func rollbackInstalledPlugins(lifecycle pluginLifecycle, results []plugin.InstallResult, cause error, restoreErrors ...string) error {
	rollbackErrors := append([]string(nil), restoreErrors...)
	for n := len(results) - 1; n >= 0; n-- {
		if rollbackErr := lifecycle.Uninstall(results[n].ID); rollbackErr != nil {
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", results[n].ID, rollbackErr))
		}
	}
	if len(rollbackErrors) > 0 {
		return fmt.Errorf("%w (rollback also failed: %s)", cause, strings.Join(rollbackErrors, "; "))
	}
	return fmt.Errorf("%w (installation rolled back)", cause)
}

type installArgs struct {
	source     string
	id         string
	installAll bool
}

func parseInstallArgs(args []string) (source, id string, installAll, ok bool) {
	if len(args) < 2 || len(args) > 4 || args[0] != "install" {
		return "", "", false, false
	}
	parsed := installArgs{}
	for n := 1; n < len(args); n++ {
		next, valid := parseInstallArg(args, n, &parsed)
		if !valid {
			return "", "", false, false
		}
		n = next
	}
	if parsed.source == "" || (parsed.id != "" && !installPluginIDPattern.MatchString(parsed.id)) || (parsed.id != "" && parsed.installAll) {
		return "", "", false, false
	}
	return parsed.source, parsed.id, parsed.installAll, true
}

func parseInstallArg(args []string, index int, parsed *installArgs) (next int, ok bool) {
	arg := args[index]
	switch {
	case arg == "--id":
		if parsed.id != "" || index+1 >= len(args) {
			return index, false
		}
		parsed.id = args[index+1]
		return index + 1, true
	case strings.HasPrefix(arg, "--id="):
		if parsed.id != "" {
			return index, false
		}
		parsed.id = strings.TrimPrefix(arg, "--id=")
		return index, true
	case arg == "--all":
		if parsed.installAll {
			return index, false
		}
		parsed.installAll = true
		return index, true
	case strings.HasPrefix(arg, "-") || parsed.source != "":
		return index, false
	default:
		parsed.source = arg
		return index, true
	}
}

func runPluginUpgrade(ctx context.Context, args []string, lifecycle pluginLifecycle, stdout, stderr io.Writer) int {
	id, source, upgradeAll, ok := parseUpgradeArgs(args)
	if !ok {
		fmt.Fprintln(stderr, "Usage: hash plugin upgrade <id> [github-repository-or-artifact-url]\n       hash plugin upgrade --all [github-repository-or-artifact-url]")
		return 2
	}
	if upgradeAll {
		upgrader, supportsAll := lifecycle.(pluginAllUpgrader)
		if !supportsAll {
			fmt.Fprintln(stderr, "hash: this installer does not support upgrading all plugins")
			return 1
		}
		allResults, err := upgrader.UpgradeAll(ctx, source)
		if err != nil {
			fmt.Fprintf(stderr, "hash: upgrade all plugins: %v\n", err)
			return 1
		}
		for n := range allResults.Results {
			result := &allResults.Results[n]
			if result.Changed {
				fmt.Fprintf(stdout, "Upgraded %s %s -> %s\n", result.ID, result.PreviousVersion, result.Version)
			} else {
				fmt.Fprintf(stdout, "%s is already at %s\n", result.ID, result.Version)
			}
		}
		for _, skipped := range allResults.Skipped {
			fmt.Fprintf(stdout, "Skipped %s: not managed by Hash (left unchanged)\n", skipped)
		}
		for _, failure := range allResults.Failures {
			fmt.Fprintf(stderr, "hash: upgrade %s: %v\n", failure.ID, failure.Err)
		}
		if len(allResults.Results) > 0 {
			fmt.Fprintln(stdout, "Restart Hash to load upgraded plugin processes.")
		} else if len(allResults.Skipped) == 0 {
			fmt.Fprintln(stdout, "No Hash-managed plugins installed.")
		}
		if len(allResults.Failures) > 0 {
			return 1
		}
		return 0
	}
	result, err := lifecycle.Upgrade(ctx, id, source)
	if err != nil {
		fmt.Fprintf(stderr, "hash: upgrade plugin: %v\n", err)
		return 1
	}
	if !result.Changed {
		fmt.Fprintf(stdout, "%s is already at %s\n", result.ID, result.Version)
		return 0
	}
	fmt.Fprintf(stdout, "Upgraded %s %s -> %s\nRestart Hash to load the new plugin process.\n", result.ID, result.PreviousVersion, result.Version)
	return 0
}

func parseUpgradeArgs(args []string) (id, source string, upgradeAll, ok bool) {
	if len(args) < 2 || len(args) > 3 || args[0] != "upgrade" {
		return "", "", false, false
	}
	if args[1] == "--all" {
		if len(args) == 3 && strings.HasPrefix(args[2], "-") {
			return "", "", false, false
		}
		if len(args) == 3 {
			source = args[2]
		}
		return "", source, true, true
	}
	if strings.HasPrefix(args[1], "-") {
		return "", "", false, false
	}
	if len(args) == 3 {
		source = args[2]
	}
	return args[1], source, false, true
}

func runPluginUninstall(args []string, lifecycle pluginLifecycle, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "Usage: hash plugin uninstall <id>")
		return 2
	}
	if err := lifecycle.Uninstall(args[1]); err != nil {
		fmt.Fprintf(stderr, "hash: uninstall plugin: %v\n", err)
		return 1
	}
	if err := config.SetPluginEnabled(getConfigDir(), args[1], false); err != nil {
		fmt.Fprintf(stderr, "hash: disable uninstalled plugin: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Uninstalled %s\n", args[1])
	return 0
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
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // XDG user data directories are conventionally world-readable
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
	for i := range manifests {
		installed[manifests[i].ID] = manifests[i]
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
		client, err := plugin.StartProcessWithSession(ctx, manifest, cfg.Plugins.Settings[id], nil, plugin.SessionContext{CWD: "", Dialect: cfg.Shell.Dialect, Kind: "doctor"})
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
	for i := range manifests {
		if manifests[i].ID == id {
			return manifests[i], true
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
	fmt.Fprintln(w, "Usage: hash plugin <list|inspect|install|upgrade|uninstall|enable|disable|link|doctor> [arguments]")
	fmt.Fprintln(w, "Install: hash plugin install --id <plugin-id> <source> | hash plugin install --all <source>")
	fmt.Fprintln(w, "Upgrade: hash plugin upgrade <plugin-id> [source] | hash plugin upgrade --all [source]")
}
