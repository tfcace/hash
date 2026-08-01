package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PluginRoots returns only the user and system data locations permitted for
// plugin discovery. Repository-local directories are intentionally excluded.
func PluginRoots() []string {
	userRoot := UserPluginRoot()
	roots := make([]string, 0, 3)
	if userRoot != "" {
		roots = append(roots, userRoot)
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, dir := range strings.Split(dataDirs, ":") {
		if dir != "" {
			roots = append(roots, filepath.Join(dir, "hash", "plugins"))
		}
	}
	return roots
}

// UserPluginRoot returns the writable user discovery root used by link and
// managed installation commands.
func UserPluginRoot() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	if dataHome == "" {
		return ""
	}
	return filepath.Join(dataHome, "hash", "plugins")
}

// Manager selects enabled plugins in configuration order and owns their warm
// process clients for one interactive Hash session.
type Manager struct {
	mu      sync.RWMutex
	enabled []*managedPlugin
	host    HostServiceHandler
	session SessionContext
}

type managedPlugin struct {
	manifest Manifest
	settings map[string]any
	client   *ProcessClient
	failures int
	disabled bool
	restarts int
}

// NewManager validates enablement against the discovered manifests. Ordering is
// preserved exactly as it appears in [plugins].enabled.
func NewManager(manifests []Manifest, enabled []string, settings map[string]map[string]any) (*Manager, error) {
	available := make(map[string]Manifest, len(manifests))
	for i := range manifests {
		manifest := &manifests[i]
		if _, exists := available[manifest.ID]; exists {
			return nil, fmt.Errorf("duplicate plugin ID %q", manifest.ID)
		}
		available[manifest.ID] = *manifest
	}
	manager := &Manager{}
	seen := make(map[string]struct{}, len(enabled))
	for _, id := range enabled {
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("plugin %q is enabled more than once", id)
		}
		seen[id] = struct{}{}
		manifest, exists := available[id]
		if !exists {
			return nil, fmt.Errorf("enabled plugin %q was not discovered", id)
		}
		pluginSettings := map[string]any{}
		for key, value := range settings[id] {
			pluginSettings[key] = value
		}
		manager.enabled = append(manager.enabled, &managedPlugin{manifest: manifest, settings: pluginSettings})
	}
	return manager, nil
}

// DiscoverManager performs installation discovery and applies user enablement.
func DiscoverManager(enabled []string, settings map[string]map[string]any) (*Manager, error) {
	manifests, err := Discover(PluginRoots())
	if err != nil {
		return nil, err
	}
	return NewManager(manifests, enabled, settings)
}

// EnabledIDs reports the configured priority order.
func (m *Manager) EnabledIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, len(m.enabled))
	for i, plugin := range m.enabled {
		ids[i] = plugin.manifest.ID
	}
	return ids
}

// Start launches all selected plugin processes. It is called only after an
// interactive session has completed startup processing.
func (m *Manager) Start(ctx context.Context) error {
	return m.StartWithHandler(ctx, nil)
}

// StartWithHandler launches selected plugins with bidirectional host services.
func (m *Manager) StartWithHandler(ctx context.Context, host HostServiceHandler) error {
	cwd, _ := os.Getwd()
	return m.StartWithSession(ctx, host, SessionContext{CWD: cwd, Dialect: "bash"})
}

// StartWithSession launches selected plugins with the current shell context.
func (m *Manager) StartWithSession(ctx context.Context, host HostServiceHandler, session SessionContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.host = host
	m.session = session
	started := 0
	var lastErr error
	for _, plugin := range m.enabled {
		if plugin.client != nil {
			continue
		}
		client, err := StartProcessWithSession(ctx, plugin.manifest, plugin.settings, host, session)
		if err != nil {
			plugin.failures++
			lastErr = err
			continue
		}
		plugin.client = client
		plugin.failures = 0
		started++
	}
	if started == 0 && len(m.enabled) > 0 {
		return lastErr
	}
	return nil
}

// Notify broadcasts an observe-only hook in enabled-list order. A failed
// plugin is left isolated from the remaining plugins.
func (m *Manager) Notify(method string, params any) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, plugin := range m.enabled {
		if plugin.client != nil {
			_ = plugin.client.Notify(method, params)
		}
	}
}

// CallFirst asks enabled plugins that declare method, in priority order, until
// a handler returns a result. The caller supplies the hook deadline.
func (m *Manager) CallFirst(ctx context.Context, method string, params, result any) (bool, error) {
	return m.CallFirstValid(ctx, method, params, nil, result)
}

// CallFirstValid asks every declaring plugin concurrently, then selects the
// first result accepted by validate in enabled-list priority order.
func (m *Manager) CallFirstValid(ctx context.Context, method string, params any, validate func(json.RawMessage) bool, result any) (bool, error) { //nolint:gocyclo // concurrent priority selection and health accounting share one bounded dispatch loop
	type target struct {
		index  int
		plugin *managedPlugin
		client *ProcessClient
	}
	m.mu.RLock()
	var targets []target
	for index, candidate := range m.enabled {
		if candidate.client != nil && !candidate.disabled && declaresHook(candidate.manifest, method) {
			targets = append(targets, target{index: index, plugin: candidate, client: candidate.client})
		}
	}
	m.mu.RUnlock()
	if len(targets) == 0 {
		return false, nil
	}
	type outcome struct {
		index int
		raw   json.RawMessage
		err   error
	}
	ch := make(chan outcome, len(targets))
	for _, t := range targets {
		go func(t target) {
			var raw json.RawMessage
			err := t.client.Call(ctx, method, params, &raw)
			ch <- outcome{index: t.index, raw: raw, err: err}
		}(t)
	}
	outcomes := make(map[int]outcome, len(targets))
	deadlineErr := error(nil)
	for len(outcomes) < len(targets) {
		select {
		case out := <-ch:
			outcomes[out.index] = out
		case <-ctx.Done():
			deadlineErr = ctx.Err()
			for _, target := range targets {
				if _, exists := outcomes[target.index]; !exists {
					outcomes[target.index] = outcome{index: target.index, err: ctx.Err()}
				}
			}
		}
	}
	for _, t := range targets {
		out := outcomes[t.index]
		m.mu.Lock()
		if out.err != nil {
			t.plugin.failures++
			exited := t.client.Exited()
			if exited && t.plugin.restarts >= 1 {
				t.plugin.disabled = true
			} else if t.plugin.failures >= 3 {
				t.plugin.disabled = true
			}
		} else {
			t.plugin.failures = 0
		}
		m.mu.Unlock()
		if out.err != nil && t.client.Exited() {
			m.restartOnce(ctx, t.plugin, t.client)
		}
		if out.err == nil {
			if validate != nil && !validate(out.raw) {
				continue
			}
			if result != nil && len(out.raw) > 0 {
				if err := json.Unmarshal(out.raw, result); err != nil {
					continue
				}
			}
			return true, nil
		}
	}
	return false, deadlineErr
}

func (m *Manager) restartOnce(ctx context.Context, target *managedPlugin, failedClient *ProcessClient) {
	m.mu.Lock()
	if target.disabled || target.client != failedClient || target.restarts >= 1 {
		m.mu.Unlock()
		return
	}
	target.restarts++
	manifest, settings := target.manifest, target.settings
	host, session := m.host, m.session
	m.mu.Unlock()

	client, err := StartProcessWithSession(ctx, manifest, settings, host, session)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		target.disabled = true
		return
	}
	if target.client == failedClient && !target.disabled {
		target.client = client
		return
	}
	_ = client.Close()
}

func declaresHook(manifest Manifest, method string) bool {
	for _, hook := range manifest.Hooks {
		if hook == method {
			return true
		}
	}
	return false
}

// Close sends shutdown to all warm processes and releases them.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
	return nil
}

func (m *Manager) closeLocked() {
	for _, plugin := range m.enabled {
		if plugin.client != nil {
			_ = plugin.client.Close()
			plugin.client = nil
		}
	}
}
