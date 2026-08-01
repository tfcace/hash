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
	"strings"
	"sync"
	"testing"
)

type releaseFixture struct {
	version  string
	archive  []byte
	checksum string
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
			fmt.Fprintf(w, "%s  hash-autocorrection_%s_darwin_arm64.tar.gz\n", fixture.checksum, fixture.version)
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
			{"name": "SHA256SUMS", "browser_download_url": r.server.URL + "/assets/" + tag + "/SHA256SUMS"},
		},
	})
}

func (r *releaseServer) setLatest(tag string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latest = tag
}

func bundleArchive(t *testing.T, version string, extra map[string]string) releaseFixture {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	files := map[string]struct {
		body string
		mode int64
	}{
		ManifestFilename: {
			body: fmt.Sprintf("manifest_version = 1\nid = \"io.runhash.autocorrection\"\nname = \"Autocorrection\"\nversion = %q\nprotocol_version = 1\nentrypoint = \"hash-autocorrection\"\nhooks = [\"command.finished\"]\n", version),
			mode: 0o644,
		},
		"hash-autocorrection": {body: "#!/bin/sh\nexit 0\n", mode: 0o755},
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
