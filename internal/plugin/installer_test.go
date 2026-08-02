package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

type releaseFixture struct {
	version  string
	archive  []byte
	checksum string
}

type catalogPluginFixture struct {
	id, version, releaseTag, artifactName string
	archive                               []byte
	checksum                              string
}

type releaseServer struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.RWMutex
	latest string
	byTag  map[string]releaseFixture
}

func newReleaseServer(t *testing.T, fixtures ...releaseFixture) *releaseServer {
	t.Helper()
	r := &releaseServer{t: t, byTag: make(map[string]releaseFixture)}
	for _, fixture := range fixtures {
		r.byTag["v"+fixture.version] = fixture
		r.latest = "v" + fixture.version
	}
	r.server = httptest.NewServer(http.HandlerFunc(r.serveHTTP))
	t.Cleanup(r.server.Close)
	return r
}

func (r *releaseServer) serveHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tag := ""
	switch {
	case strings.HasSuffix(req.URL.Path, "/releases/latest"):
		tag = r.latest
	case strings.Contains(req.URL.Path, "/releases/tags/"):
		tag = strings.TrimPrefix(req.URL.Path, "/repos/owner/repo/releases/tags/")
	case strings.HasPrefix(req.URL.Path, "/assets/"):
		parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/assets/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, req)
			return
		}
		fixture, ok := r.byTag[parts[0]]
		if !ok {
			http.NotFound(w, req)
			return
		}
		if parts[1] == "SHA256SUMS" {
			index := testReleaseIndex(r.t, fixture.version)
			digest := sha256.Sum256(index)
			fmt.Fprintf(w, "%s  hash-autocorrection_%s_darwin_arm64.tar.gz\n%s  HASH_PLUGINS.json\n", fixture.checksum, fixture.version, hex.EncodeToString(digest[:]))
			return
		}
		if parts[1] == "HASH_PLUGINS.json" {
			_, _ = w.Write(testReleaseIndex(r.t, fixture.version))
			return
		}
		_, _ = w.Write(fixture.archive)
		return
	default:
		http.NotFound(w, req)
		return
	}
	fixture, ok := r.byTag[tag]
	if !ok {
		http.NotFound(w, req)
		return
	}
	assetName := fmt.Sprintf("hash-autocorrection_%s_darwin_arm64.tar.gz", fixture.version)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tag_name": tag,
		"assets": []map[string]string{
			{"name": assetName, "browser_download_url": r.server.URL + "/assets/" + tag + "/archive"},
			{"name": "HASH_PLUGINS.json", "browser_download_url": r.server.URL + "/assets/" + tag + "/HASH_PLUGINS.json"},
			{"name": "SHA256SUMS", "browser_download_url": r.server.URL + "/assets/" + tag + "/SHA256SUMS"},
		},
	})
}

func testReleaseIndex(t *testing.T, version string) []byte {
	t.Helper()
	data, err := json.Marshal(githubReleaseIndex{
		SchemaVersion: 2,
		Plugins: map[string]githubPluginRelease{
			"io.runhash.autocorrection": {
				Version:    version,
				ReleaseTag: "v" + version,
				Artifacts: map[string]githubReleaseArtifact{
					"darwin/arm64": {Name: "hash-autocorrection_{{version}}_darwin_arm64.tar.gz"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func newCatalogOnlyReleaseServer(t *testing.T, fixtures ...catalogPluginFixture) *httptest.Server {
	t.Helper()
	plugins := make(map[string]githubPluginRelease, len(fixtures))
	byTag := make(map[string]catalogPluginFixture, len(fixtures))
	for _, fixture := range fixtures {
		plugins[fixture.id] = githubPluginRelease{Version: fixture.version, ReleaseTag: fixture.releaseTag, Artifacts: map[string]githubReleaseArtifact{
			"darwin/arm64": {Name: fixture.artifactName},
		}}
		byTag[fixture.releaseTag] = fixture
	}
	indexData, err := json.Marshal(githubReleaseIndex{SchemaVersion: 2, Plugins: plugins})
	if err != nil {
		t.Fatal(err)
	}
	indexDigest := sha256.Sum256(indexData)
	catalogChecksums := []byte(hex.EncodeToString(indexDigest[:]) + "  HASH_PLUGINS.json\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		tag := ""
		switch {
		case req.URL.Path == "/repos/owner/repo/releases/latest":
			tag = "catalog-v1"
		case strings.HasPrefix(req.URL.Path, "/repos/owner/repo/releases/tags/"):
			tag = strings.TrimPrefix(req.URL.Path, "/repos/owner/repo/releases/tags/")
		}
		if tag != "" {
			assets := []map[string]string{
				{"name": "HASH_PLUGINS.json", "browser_download_url": "http://" + req.Host + "/assets/" + tag + "/HASH_PLUGINS.json"},
				{"name": "SHA256SUMS", "browser_download_url": "http://" + req.Host + "/assets/" + tag + "/SHA256SUMS"},
			}
			if fixture, ok := byTag[tag]; ok {
				assets = append([]map[string]string{{"name": fixture.artifactName, "browser_download_url": "http://" + req.Host + "/assets/" + tag + "/" + fixture.artifactName}}, assets...)
			} else if tag != "catalog-v1" {
				http.NotFound(w, req)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": assets})
			return
		}
		parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/assets/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, req)
			return
		}
		if parts[1] == "HASH_PLUGINS.json" {
			_, _ = w.Write(indexData)
			return
		}
		if parts[1] == "SHA256SUMS" {
			if fixture, ok := byTag[parts[0]]; ok {
				fmt.Fprintf(w, "%s  %s\n%s", fixture.checksum, fixture.artifactName, catalogChecksums)
				return
			}
			_, _ = w.Write(catalogChecksums)
			return
		}
		fixture, ok := byTag[parts[0]]
		if !ok || parts[1] != fixture.artifactName {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(fixture.archive)
	}))
	t.Cleanup(server.Close)
	return server
}

func (r *releaseServer) setLatest(tag string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latest = tag
}

func bundleArchive(t *testing.T, version string, extra map[string]string) releaseFixture {
	return bundleArchiveFor(t, "io.runhash.autocorrection", "Autocorrection", "hash-autocorrection", version, extra)
}

func bundleArchiveFor(t *testing.T, id, name, executable, version string, extra map[string]string) releaseFixture {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	files := map[string]struct {
		body string
		mode int64
	}{
		ManifestFilename: {
			body: fmt.Sprintf("manifest_version = 1\nid = %q\nname = %q\nversion = %q\nprotocol_version = 1\nentrypoint = %q\nhooks = [\"command.finished\"]\n", id, name, version, executable),
			mode: 0o644,
		},
		executable: {body: "#!/bin/sh\nexit 0\n", mode: 0o755},
	}
	for name, body := range extra {
		files[name] = struct {
			body string
			mode int64
		}{body: body, mode: 0o644}
	}
	for name, file := range files {
		header := &tar.Header{Name: name, Mode: file.mode, Size: int64(len(file.body)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tarWriter, file.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(compressed.Bytes())
	return releaseFixture{version: version, archive: compressed.Bytes(), checksum: hex.EncodeToString(digest[:])}
}

func newIndexedReleaseServer(t *testing.T, fixtures map[string]releaseFixture) *httptest.Server {
	t.Helper()
	version := "0.1.0"
	assets := make([]map[string]string, 0, len(fixtures)+2)
	checksums := make([]string, 0, len(fixtures)+1)
	index := githubReleaseIndex{SchemaVersion: 2, Plugins: make(map[string]githubPluginRelease, len(fixtures))}
	for id, fixture := range fixtures {
		name := strings.ReplaceAll(id, ".", "-") + "_" + fixture.version + "_darwin_arm64.tar.gz"
		assets = append(assets, map[string]string{"name": name})
		checksums = append(checksums, fixture.checksum+"  "+name)
		index.Plugins[id] = githubPluginRelease{Version: fixture.version, ReleaseTag: "v" + fixture.version, Artifacts: map[string]githubReleaseArtifact{
			"darwin/arm64": {Name: strings.Replace(name, fixture.version, "{{version}}", 1)},
		}}
		version = fixture.version
	}
	indexData, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	indexDigest := sha256.Sum256(indexData)
	checksums = append(checksums, hex.EncodeToString(indexDigest[:])+"  HASH_PLUGINS.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/repos/owner/repo/releases/latest", "/repos/owner/repo/releases/tags/v" + version:
			responseAssets := make([]map[string]string, 0, len(assets)+2)
			for _, asset := range assets {
				responseAssets = append(responseAssets, map[string]string{"name": asset["name"], "browser_download_url": "http://" + req.Host + "/assets/" + asset["name"]})
			}
			responseAssets = append(responseAssets,
				map[string]string{"name": "HASH_PLUGINS.json", "browser_download_url": "http://" + req.Host + "/assets/HASH_PLUGINS.json"},
				map[string]string{"name": "SHA256SUMS", "browser_download_url": "http://" + req.Host + "/assets/SHA256SUMS"},
			)
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + version, "assets": responseAssets})
		case "/assets/HASH_PLUGINS.json":
			_, _ = w.Write(indexData)
		case "/assets/SHA256SUMS":
			_, _ = io.WriteString(w, strings.Join(checksums, "\n")+"\n")
		default:
			name := strings.TrimPrefix(req.URL.Path, "/assets/")
			for id, fixture := range fixtures {
				if name == strings.ReplaceAll(id, ".", "-")+"_"+fixture.version+"_darwin_arm64.tar.gz" {
					_, _ = w.Write(fixture.archive)
					return
				}
			}
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

type indexedReleaseData struct {
	version   string
	index     []byte
	checksums []byte
	archives  map[string][]byte
}

func newLatestSwitchingIndexedReleaseServer(t *testing.T, first, second map[string]releaseFixture) *httptest.Server {
	t.Helper()
	firstRelease := newIndexedReleaseData(t, first)
	secondRelease := newIndexedReleaseData(t, second)
	releases := map[string]indexedReleaseData{
		"v" + firstRelease.version:  firstRelease,
		"v" + secondRelease.version: secondRelease,
	}
	latest := "v" + firstRelease.version
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		tag := ""
		switch {
		case req.URL.Path == "/repos/owner/repo/releases/latest":
			tag = latest
		case strings.HasPrefix(req.URL.Path, "/repos/owner/repo/releases/tags/"):
			tag = strings.TrimPrefix(req.URL.Path, "/repos/owner/repo/releases/tags/")
		}
		if tag != "" {
			release, ok := releases[tag]
			if !ok {
				http.NotFound(w, req)
				return
			}
			assets := make([]map[string]string, 0, len(release.archives)+2)
			for name := range release.archives {
				assets = append(assets, map[string]string{"name": name, "browser_download_url": "http://" + req.Host + "/assets/" + tag + "/" + name})
			}
			sort.Slice(assets, func(left, right int) bool { return assets[left]["name"] < assets[right]["name"] })
			assets = append(assets,
				map[string]string{"name": "HASH_PLUGINS.json", "browser_download_url": "http://" + req.Host + "/assets/" + tag + "/HASH_PLUGINS.json"},
				map[string]string{"name": "SHA256SUMS", "browser_download_url": "http://" + req.Host + "/assets/" + tag + "/SHA256SUMS"},
			)
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": assets})
			return
		}
		parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/assets/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, req)
			return
		}
		release, ok := releases[parts[0]]
		if !ok {
			http.NotFound(w, req)
			return
		}
		switch parts[1] {
		case "HASH_PLUGINS.json":
			_, _ = w.Write(release.index)
		case "SHA256SUMS":
			_, _ = w.Write(release.checksums)
		default:
			archive, ok := release.archives[parts[1]]
			if !ok {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write(archive)
			switch parts[0] {
			case "v" + firstRelease.version:
				latest = "v" + secondRelease.version
			case "v" + secondRelease.version:
				latest = "v" + firstRelease.version
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newIndexedReleaseData(t *testing.T, fixtures map[string]releaseFixture) indexedReleaseData {
	t.Helper()
	ids := make([]string, 0, len(fixtures))
	for id := range fixtures {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		t.Fatal("indexed release requires at least one plugin")
	}
	release := indexedReleaseData{version: fixtures[ids[0]].version, archives: make(map[string][]byte, len(fixtures))}
	index := githubReleaseIndex{SchemaVersion: 2, Plugins: make(map[string]githubPluginRelease, len(fixtures))}
	checksums := make([]string, 0, len(fixtures)+1)
	for _, id := range ids {
		fixture := fixtures[id]
		if fixture.version != release.version {
			t.Fatalf("mixed fixture versions: %s and %s", fixture.version, release.version)
		}
		name := strings.ReplaceAll(id, ".", "-") + "_" + fixture.version + "_darwin_arm64.tar.gz"
		release.archives[name] = fixture.archive
		checksums = append(checksums, fixture.checksum+"  "+name)
		index.Plugins[id] = githubPluginRelease{Version: fixture.version, ReleaseTag: "v" + fixture.version, Artifacts: map[string]githubReleaseArtifact{
			"darwin/arm64": {Name: strings.Replace(name, fixture.version, "{{version}}", 1)},
		}}
	}
	indexData, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(indexData)
	checksums = append(checksums, hex.EncodeToString(digest[:])+"  HASH_PLUGINS.json")
	release.index = indexData
	release.checksums = []byte(strings.Join(checksums, "\n") + "\n")
	return release
}

func testInstaller(t *testing.T, server *releaseServer) *Installer {
	t.Helper()
	data := t.TempDir()
	installer := NewInstaller(filepath.Join(data, "plugins"), filepath.Join(data, "plugin-bundles"))
	installer.githubAPIBase = server.server.URL
	installer.allowHTTP = true
	installer.goos = "darwin"
	installer.goarch = "arm64"
	return installer
}

func TestInstallerGitHubInstallUninstallAndReinstall(t *testing.T) {
	server := newReleaseServer(t, bundleArchive(t, "0.1.0", nil))
	installer := testInstaller(t, server)

	result, err := installer.Install(t.Context(), "github:owner/repo@v0.1.0")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if result.ID != "io.runhash.autocorrection" || result.Version != "0.1.0" {
		t.Fatalf("Install() = %+v", result)
	}
	active := filepath.Join(installer.pluginRoot, result.ID)
	if info, err := os.Lstat(active); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("active plugin is not a symlink: info=%v err=%v", info, err)
	}
	manifest, err := LoadManifest(active)
	if err != nil || manifest.Version != "0.1.0" {
		t.Fatalf("active manifest = %+v, err=%v", manifest, err)
	}
	if result.Source != "github:owner/repo" {
		t.Fatalf("canonical source = %q", result.Source)
	}

	if err := installer.Uninstall(result.ID); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if _, err := os.Lstat(active); !os.IsNotExist(err) {
		t.Fatalf("active plugin remains after uninstall: %v", err)
	}
	if _, err := installer.Install(t.Context(), "github:owner/repo@v0.1.0"); err != nil {
		t.Fatalf("reinstall error = %v", err)
	}
}

func TestInstallerUpgradeUsesSavedUnpinnedGitHubSource(t *testing.T) {
	server := newReleaseServer(t, bundleArchive(t, "0.1.0", nil), bundleArchive(t, "0.1.1", nil))
	server.setLatest("v0.1.0")
	installer := testInstaller(t, server)
	if _, err := installer.Install(t.Context(), "github:owner/repo@v0.1.0"); err != nil {
		t.Fatal(err)
	}
	server.setLatest("v0.1.1")

	result, err := installer.Upgrade(t.Context(), "io.runhash.autocorrection", "")
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if result.PreviousVersion != "0.1.0" || result.Version != "0.1.1" || !result.Changed {
		t.Fatalf("Upgrade() = %+v", result)
	}
	manifest, err := LoadManifest(filepath.Join(installer.pluginRoot, result.ID))
	if err != nil || manifest.Version != "0.1.1" {
		t.Fatalf("upgraded manifest = %+v, err=%v", manifest, err)
	}
	if _, err := os.Stat(filepath.Join(installer.bundleRoot, result.ID, "0.1.0")); err != nil {
		t.Fatalf("previous version was not retained: %v", err)
	}
}

func TestInstallerInstallAllUsesSortedIndexAndBareInstallRequiresChoice(t *testing.T) {
	server := newIndexedReleaseServer(t, map[string]releaseFixture{
		"io.runhash.beta":  bundleArchiveFor(t, "io.runhash.beta", "Beta", "hash-beta", "0.1.0", nil),
		"io.runhash.alpha": bundleArchiveFor(t, "io.runhash.alpha", "Alpha", "hash-alpha", "0.1.0", nil),
	})
	data := t.TempDir()
	installer := NewInstaller(filepath.Join(data, "plugins"), filepath.Join(data, "plugin-bundles"))
	installer.githubAPIBase = server.URL
	installer.allowHTTP = true
	installer.goos, installer.goarch = "darwin", "arm64"

	if _, err := installer.Install(t.Context(), "github:owner/repo"); err == nil || !strings.Contains(err.Error(), "--id") || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("bare Install() error = %v", err)
	}
	results, err := installer.InstallAll(t.Context(), "github:owner/repo")
	if err != nil {
		t.Fatalf("InstallAll() error = %v", err)
	}
	if len(results) != 2 || results[0].ID != "io.runhash.alpha" || results[1].ID != "io.runhash.beta" {
		t.Fatalf("InstallAll() results = %+v", results)
	}
}

func TestInstallerInstallAllRollsBackEarlierBundlesOnFailure(t *testing.T) {
	server := newIndexedReleaseServer(t, map[string]releaseFixture{
		"io.runhash.alpha": bundleArchiveFor(t, "io.runhash.alpha", "Alpha", "hash-alpha", "0.1.0", nil),
		"io.runhash.beta": bundleArchiveFor(t, "io.runhash.beta", "Beta", "hash-beta", "0.1.0", map[string]string{
			ManifestFilename: "manifest_version = 1\nid = \"invalid\"\n",
		}),
	})
	data := t.TempDir()
	installer := NewInstaller(filepath.Join(data, "plugins"), filepath.Join(data, "plugin-bundles"))
	installer.githubAPIBase = server.URL
	installer.allowHTTP = true
	installer.goos, installer.goarch = "darwin", "arm64"

	if results, err := installer.InstallAll(t.Context(), "github:owner/repo"); err == nil || results != nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("InstallAll() results=%+v error=%v", results, err)
	}
	if _, err := os.Lstat(filepath.Join(installer.pluginRoot, "io.runhash.alpha")); !os.IsNotExist(err) {
		t.Fatalf("successful earlier bundle remained active after failure: %v", err)
	}
}

func TestInstallerInstallAllPinsOneReleaseSnapshot(t *testing.T) {
	versionOne := map[string]releaseFixture{
		"io.runhash.alpha": bundleArchiveFor(t, "io.runhash.alpha", "Alpha", "hash-alpha", "0.1.0", nil),
		"io.runhash.beta":  bundleArchiveFor(t, "io.runhash.beta", "Beta", "hash-beta", "0.1.0", nil),
	}
	versionTwo := map[string]releaseFixture{
		"io.runhash.alpha": bundleArchiveFor(t, "io.runhash.alpha", "Alpha", "hash-alpha", "0.2.0", nil),
		"io.runhash.beta":  bundleArchiveFor(t, "io.runhash.beta", "Beta", "hash-beta", "0.2.0", nil),
	}
	server := newLatestSwitchingIndexedReleaseServer(t, versionOne, versionTwo)
	data := t.TempDir()
	installer := NewInstaller(filepath.Join(data, "plugins"), filepath.Join(data, "plugin-bundles"))
	installer.githubAPIBase = server.URL
	installer.allowHTTP = true
	installer.goos, installer.goarch = "darwin", "arm64"

	results, err := installer.InstallAll(t.Context(), "github:owner/repo")
	if err != nil {
		t.Fatalf("InstallAll() error = %v", err)
	}
	if len(results) != 2 || results[0].Version != "0.1.0" || results[1].Version != "0.1.0" {
		t.Fatalf("InstallAll() mixed release snapshots: %+v", results)
	}
}

func TestInstallerCatalogReleaseWithoutArtifactsResolvesIndependentPluginTags(t *testing.T) {
	alpha := bundleArchiveFor(t, "io.runhash.alpha", "Alpha", "hash-alpha", "1.0.0", nil)
	beta := bundleArchiveFor(t, "io.runhash.beta", "Beta", "hash-beta", "2.3.0", nil)
	server := newCatalogOnlyReleaseServer(t,
		catalogPluginFixture{id: "io.runhash.alpha", version: "1.0.0", releaseTag: "alpha-v1.0.0", artifactName: "hash-alpha_1.0.0_darwin_arm64.tar.gz", archive: alpha.archive, checksum: alpha.checksum},
		catalogPluginFixture{id: "io.runhash.beta", version: "2.3.0", releaseTag: "beta-v2.3.0", artifactName: "hash-beta_2.3.0_darwin_arm64.tar.gz", archive: beta.archive, checksum: beta.checksum},
	)
	data := t.TempDir()
	installer := NewInstaller(filepath.Join(data, "plugins"), filepath.Join(data, "plugin-bundles"))
	installer.githubAPIBase, installer.allowHTTP = server.URL, true
	installer.goos, installer.goarch = "darwin", "arm64"

	if _, err := installer.Install(t.Context(), "github:owner/repo"); err == nil || !strings.Contains(err.Error(), "--id") {
		t.Fatalf("bare multi-plugin Install() error = %v", err)
	}
	results, err := installer.InstallAll(t.Context(), "github:owner/repo")
	if err != nil {
		t.Fatalf("InstallAll() error = %v", err)
	}
	if len(results) != 2 || results[0].ID != "io.runhash.alpha" || results[0].Version != "1.0.0" || results[1].ID != "io.runhash.beta" || results[1].Version != "2.3.0" {
		t.Fatalf("InstallAll() independent releases = %+v", results)
	}
}

func TestInstallerUpgradeAllPinsOneCatalogSnapshot(t *testing.T) {
	versionOne := map[string]releaseFixture{
		"io.runhash.alpha": bundleArchiveFor(t, "io.runhash.alpha", "Alpha", "hash-alpha", "0.1.0", nil),
		"io.runhash.beta":  bundleArchiveFor(t, "io.runhash.beta", "Beta", "hash-beta", "0.1.0", nil),
	}
	versionTwo := map[string]releaseFixture{
		"io.runhash.alpha": bundleArchiveFor(t, "io.runhash.alpha", "Alpha", "hash-alpha", "0.2.0", nil),
		"io.runhash.beta":  bundleArchiveFor(t, "io.runhash.beta", "Beta", "hash-beta", "0.2.0", nil),
	}
	server := newLatestSwitchingIndexedReleaseServer(t, versionOne, versionTwo)
	data := t.TempDir()
	installer := NewInstaller(filepath.Join(data, "plugins"), filepath.Join(data, "plugin-bundles"))
	installer.githubAPIBase, installer.allowHTTP = server.URL, true
	installer.goos, installer.goarch = "darwin", "arm64"
	if _, err := installer.InstallAll(t.Context(), "github:owner/repo"); err != nil {
		t.Fatal(err)
	}

	result, err := installer.UpgradeAll(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || result.Results[0].Version != "0.2.0" || result.Results[1].Version != "0.2.0" {
		t.Fatalf("UpgradeAll() mixed catalog snapshots: %+v", result)
	}
}

func TestInstallerUpgradeAllRollsBackEarlierChangesWhenLaterPluginFails(t *testing.T) {
	first := map[string]releaseFixture{
		"io.runhash.alpha": bundleArchiveFor(t, "io.runhash.alpha", "Alpha", "hash-alpha", "0.1.0", nil),
		"io.runhash.beta":  bundleArchiveFor(t, "io.runhash.beta", "Beta", "hash-beta", "0.1.0", nil),
	}
	alphaUpgrade := bundleArchiveFor(t, "io.runhash.alpha", "Alpha", "hash-alpha", "0.2.0", nil)
	betaUpgrade := bundleArchiveFor(t, "io.runhash.beta", "Beta", "hash-beta", "0.2.0", nil)
	// Keep the published checksum while serving a different archive so the
	// second upgrade fails after the first plugin has already switched.
	betaUpgrade.archive = append([]byte(nil), betaUpgrade.archive[:len(betaUpgrade.archive)-1]...)
	second := map[string]releaseFixture{
		"io.runhash.alpha": alphaUpgrade,
		"io.runhash.beta":  betaUpgrade,
	}
	server := newLatestSwitchingIndexedReleaseServer(t, first, second)
	data := t.TempDir()
	installer := NewInstaller(filepath.Join(data, "plugins"), filepath.Join(data, "plugin-bundles"))
	installer.githubAPIBase, installer.allowHTTP = server.URL, true
	installer.goos, installer.goarch = "darwin", "arm64"
	if _, err := installer.InstallAll(t.Context(), "github:owner/repo"); err != nil {
		t.Fatal(err)
	}

	result, err := installer.UpgradeAll(t.Context(), "github:owner/repo@v0.2.0")
	if err != nil {
		t.Fatalf("UpgradeAll() error = %v", err)
	}
	if len(result.Failures) != 1 || result.Failures[0].ID != "io.runhash.beta" {
		t.Fatalf("UpgradeAll() failures = %+v", result.Failures)
	}

	for _, id := range []string{"io.runhash.alpha", "io.runhash.beta"} {
		manifest, err := LoadManifest(filepath.Join(installer.pluginRoot, id))
		if err != nil {
			t.Fatalf("LoadManifest(%s) error = %v", id, err)
		}
		if manifest.Version != "0.1.0" {
			t.Fatalf("active %s version = %s, want rollback to 0.1.0", id, manifest.Version)
		}
		if _, err := os.Stat(filepath.Join(installer.bundleRoot, id, "0.2.0")); !os.IsNotExist(err) {
			t.Fatalf("new %s bundle remains after rollback: %v", id, err)
		}
	}
}

func TestInstallerUpgradeAllSkipsDeveloperLinks(t *testing.T) {
	server := newReleaseServer(t, bundleArchive(t, "0.1.0", nil), bundleArchive(t, "0.1.1", nil))
	server.setLatest("v0.1.0")
	installer := testInstaller(t, server)
	if _, err := installer.Install(t.Context(), "github:owner/repo@v0.1.0"); err != nil {
		t.Fatal(err)
	}
	developerBundle := t.TempDir()
	if err := os.Symlink(developerBundle, filepath.Join(installer.pluginRoot, "io.runhash.developer")); err != nil {
		t.Fatal(err)
	}
	server.setLatest("v0.1.1")

	result, err := installer.UpgradeAll(t.Context(), "")
	if err != nil {
		t.Fatalf("UpgradeAll() error = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].ID != "io.runhash.autocorrection" || !result.Results[0].Changed {
		t.Fatalf("UpgradeAll() results = %+v", result)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "io.runhash.developer" {
		t.Fatalf("UpgradeAll() skipped = %+v", result.Skipped)
	}
	if target, err := os.Readlink(filepath.Join(installer.pluginRoot, "io.runhash.developer")); err != nil || target != developerBundle {
		t.Fatalf("developer link changed: target=%q err=%v", target, err)
	}
}

func TestInstallerFailedUpgradeKeepsPreviousVersionActive(t *testing.T) {
	good := bundleArchive(t, "0.1.0", nil)
	bad := bundleArchive(t, "0.1.1", map[string]string{
		ManifestFilename: "manifest_version = 1\nid = \"invalid\"\n",
	})
	server := newReleaseServer(t, good, bad)
	server.setLatest("v0.1.0")
	installer := testInstaller(t, server)
	if _, err := installer.Install(t.Context(), "github:owner/repo@v0.1.0"); err != nil {
		t.Fatal(err)
	}
	server.setLatest("v0.1.1")

	if _, err := installer.Upgrade(t.Context(), "io.runhash.autocorrection", ""); err == nil {
		t.Fatal("Upgrade() succeeded with an invalid bundle")
	}
	manifest, err := LoadManifest(filepath.Join(installer.pluginRoot, "io.runhash.autocorrection"))
	if err != nil || manifest.Version != "0.1.0" {
		t.Fatalf("failed upgrade changed active plugin: %+v, err=%v", manifest, err)
	}
}

func TestInstallerDirectHTTPSArtifactRequiresMatchingChecksum(t *testing.T) {
	fixture := bundleArchive(t, "0.1.0", nil)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture.archive)
	}))
	t.Cleanup(server.Close)
	data := t.TempDir()
	installer := NewInstaller(filepath.Join(data, "plugins"), filepath.Join(data, "plugin-bundles"))
	installer.client = server.Client()

	source := server.URL + "/plugin.tar.gz#sha256=" + fixture.checksum
	if _, err := installer.Install(t.Context(), source); err != nil {
		t.Fatalf("Install() direct URL error = %v", err)
	}
	if err := installer.Uninstall("io.runhash.autocorrection"); err != nil {
		t.Fatal(err)
	}
	bad := server.URL + "/plugin.tar.gz#sha256=" + strings.Repeat("0", 64)
	if _, err := installer.Install(t.Context(), bad); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("bad checksum error = %v", err)
	}
}

func TestInstallerRejectsArchiveTraversalWithoutWritingOutsideStaging(t *testing.T) {
	fixture := bundleArchive(t, "0.1.0", map[string]string{"../escaped": "unsafe"})
	server := newReleaseServer(t, fixture)
	installer := testInstaller(t, server)
	escaped := filepath.Join(filepath.Dir(installer.bundleRoot), "escaped")

	if _, err := installer.Install(t.Context(), "github:owner/repo@v0.1.0"); err == nil || !strings.Contains(err.Error(), "archive path") {
		t.Fatalf("traversal error = %v", err)
	}
	if _, err := os.Stat(escaped); !os.IsNotExist(err) {
		t.Fatalf("archive wrote outside staging: %v", err)
	}
}

func TestInstallerRefusesToUninstallDeveloperLink(t *testing.T) {
	data := t.TempDir()
	pluginRoot := filepath.Join(data, "plugins")
	bundleRoot := filepath.Join(data, "plugin-bundles")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	developerBundle := t.TempDir()
	if err := os.Symlink(developerBundle, filepath.Join(pluginRoot, "io.runhash.demo")); err != nil {
		t.Fatal(err)
	}
	installer := NewInstaller(pluginRoot, bundleRoot)
	if err := installer.Uninstall("io.runhash.demo"); err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("Uninstall() error = %v", err)
	}
}

func TestParseGitHubSource(t *testing.T) {
	tests := []struct {
		source, owner, repo, tag string
	}{
		{"github:owner/repo", "owner", "repo", ""},
		{"github:owner/repo@v1.2.3", "owner", "repo", "v1.2.3"},
		{"https://github.com/owner/repo", "owner", "repo", ""},
		{"https://github.com/owner/repo/releases/tag/v1.2.3", "owner", "repo", "v1.2.3"},
	}
	for _, test := range tests {
		got, ok, err := parseGitHubSource(test.source)
		if err != nil || !ok || got.owner != test.owner || got.repo != test.repo || got.tag != test.tag {
			t.Fatalf("parseGitHubSource(%q) = %+v, %v, %v", test.source, got, ok, err)
		}
	}
	if _, ok, err := parseGitHubSource("github:owner/repo@"); !ok || err == nil {
		t.Fatalf("empty GitHub tag accepted: ok=%v err=%v", ok, err)
	}
}

func TestFetchGitHubReleaseRejectsMismatchedRequestedTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/repos/owner/repo/releases/tags/v1.2.3" {
			http.NotFound(w, req)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.4",
			"assets":   []map[string]string{},
		})
	}))
	t.Cleanup(server.Close)
	data := t.TempDir()
	installer := NewInstaller(filepath.Join(data, "plugins"), filepath.Join(data, "plugin-bundles"))
	installer.githubAPIBase = server.URL
	installer.allowHTTP = true

	_, err := installer.fetchGitHubRelease(t.Context(), githubSource{owner: "owner", repo: "repo", tag: "v1.2.3"})
	if err == nil || !strings.Contains(err.Error(), "does not match requested tag") {
		t.Fatalf("fetchGitHubRelease() error = %v, want requested-tag mismatch", err)
	}
}

func TestSelectGitHubAssetsRequiresAnIndexForMultiplePlatformArtifacts(t *testing.T) {
	release := githubRelease{
		Assets: []githubAsset{
			{Name: "hash-autocorrection_0.2.1_darwin_arm64.tar.gz", URL: "artifact"},
			{Name: "hash-adaptive-prediction_0.2.1_darwin_arm64.tar.gz", URL: "other"},
			{Name: "SHA256SUMS", URL: "checksums"},
		},
	}

	if _, err := selectGitHubAssets(release, "darwin", "arm64"); err == nil {
		t.Fatal("selectGitHubAssets accepted multiple platform artifacts without an index")
	}
}

func TestSelectIndexedGitHubPluginRequiresAnExplicitChoiceForMultiplePlugins(t *testing.T) {
	index := githubReleaseIndex{Plugins: map[string]githubPluginRelease{
		"io.runhash.autocorrection":  {},
		"io.runhash.autosuggestions": {},
	}}

	_, err := selectIndexedGitHubPlugin(index, "")
	if err == nil || !strings.Contains(err.Error(), "io.runhash.autocorrection, io.runhash.autosuggestions") || !strings.Contains(err.Error(), "--id") || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("selectIndexedGitHubPlugin() error = %v", err)
	}
}

func TestSelectIndexedGitHubPluginAllowsTheOnlyPluginWithoutDefault(t *testing.T) {
	index := githubReleaseIndex{Plugins: map[string]githubPluginRelease{
		"io.runhash.autocorrection": {},
	}}

	id, err := selectIndexedGitHubPlugin(index, "")
	if err != nil || id != "io.runhash.autocorrection" {
		t.Fatalf("selectIndexedGitHubPlugin() = %q, %v", id, err)
	}
}

func TestDecodeGitHubReleaseIndexRejectsLegacySchemaAndUnsafeReleaseTag(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(`{"plugins":{"io.runhash.demo":{"version":"1.0.0","release_tag":"demo-v1","artifacts":{"darwin/arm64":{"name":"demo_1.0.0_darwin_arm64.tar.gz"}}}}}`),
		[]byte(`{"schema_version":2,"plugins":{"io.runhash.demo":{"version":"1.0.0","release_tag":"../demo-v1","artifacts":{"darwin/arm64":{"name":"demo_1.0.0_darwin_arm64.tar.gz"}}}}}`),
	} {
		if _, err := decodeGitHubReleaseIndex(data); err == nil {
			t.Fatalf("decodeGitHubReleaseIndex(%s) succeeded", data)
		}
	}
}
