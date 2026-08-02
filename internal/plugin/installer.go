package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	installMetadataFilename = ".hash-install.json"
	maxArtifactBytes        = 64 << 20
	maxExtractedBytes       = 128 << 20
	maxArchiveEntries       = 64
	maxAPIResponseBytes     = 2 << 20
	defaultGitHubAPIBase    = "https://api.github.com"
)

var installVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
var githubNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Installer manages downloaded plugin bundles separately from developer links.
type Installer struct {
	pluginRoot    string
	bundleRoot    string
	client        *http.Client
	githubAPIBase string
	goos          string
	goarch        string
	allowHTTP     bool // tests only; production constructors leave this false
}

// InstallResult describes an installed or upgraded plugin bundle.
type InstallResult struct {
	Manifest        Manifest
	ID              string
	Version         string
	PreviousVersion string
	Source          string
	ArtifactURL     string
	Checksum        string
	Changed         bool
}

// UpgradeAllResult reports every managed bundle considered by UpgradeAll.
// Developer links and malformed/unmanaged targets are skipped, never modified.
type UpgradeAllResult struct {
	Results  []InstallResult
	Skipped  []string
	Failures []UpgradeFailure
}

// UpgradeFailure identifies one managed plugin that could not be upgraded.
type UpgradeFailure struct {
	ID  string
	Err error
}

type installMetadata struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	Version       string    `json:"version"`
	Source        string    `json:"source"`
	ArtifactURL   string    `json:"artifact_url"`
	SHA256        string    `json:"sha256"`
	InstalledAt   time.Time `json:"installed_at"`
}

type resolvedArtifact struct {
	url             string
	checksum        string
	source          string
	expectedVersion string
}

// NewInstaller creates a managed installer rooted in the user's XDG data tree.
func NewInstaller(pluginRoot, bundleRoot string) *Installer {
	return &Installer{
		pluginRoot:    pluginRoot,
		bundleRoot:    bundleRoot,
		githubAPIBase: defaultGitHubAPIBase,
		goos:          runtime.GOOS,
		goarch:        runtime.GOARCH,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				if req.URL.Scheme != "https" {
					return fmt.Errorf("refusing non-HTTPS redirect")
				}
				return nil
			},
		},
	}
}

// Install downloads, validates, and atomically activates a disabled plugin.
func (i *Installer) Install(ctx context.Context, source string) (InstallResult, error) {
	return i.install(ctx, source, "", false)
}

// InstallForID selects a plugin from a multi-plugin GitHub release index.
func (i *Installer) InstallForID(ctx context.Context, source, id string) (InstallResult, error) {
	if !pluginIDPattern.MatchString(id) {
		return InstallResult{}, fmt.Errorf("invalid plugin ID %q", id)
	}
	return i.install(ctx, source, id, false)
}

// InstallAll installs every plugin published in a signed multi-plugin release.
// Each bundle is independently validated and activated atomically. The caller is
// responsible for keeping the newly installed plugins disabled.
func (i *Installer) InstallAll(ctx context.Context, source string) ([]InstallResult, error) {
	ids, pinnedSource, err := i.indexedGitHubPluginIDs(ctx, source)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := os.Lstat(filepath.Join(i.pluginRoot, id)); err == nil {
			return nil, fmt.Errorf("plugin %q is already installed; use hash plugin upgrade", id)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	results := make([]InstallResult, 0, len(ids))
	for _, id := range ids {
		result, err := i.InstallForID(ctx, pinnedSource, id)
		if err != nil {
			var rollbackErrors []string
			for n := len(results) - 1; n >= 0; n-- {
				if rollbackErr := i.Uninstall(results[n].ID); rollbackErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", results[n].ID, rollbackErr))
				}
			}
			if len(rollbackErrors) > 0 {
				return nil, fmt.Errorf("install %q: %w (rollback also failed: %s)", id, err, strings.Join(rollbackErrors, "; "))
			}
			return nil, fmt.Errorf("install %q: %w (previous installations rolled back)", id, err)
		}
		results = append(results, result)
	}
	return results, nil
}

// Upgrade resolves the saved unpinned source (or an explicit replacement),
// retains the previous bundle, and atomically switches the active symlink.
func (i *Installer) Upgrade(ctx context.Context, id, source string) (InstallResult, error) {
	if !pluginIDPattern.MatchString(id) {
		return InstallResult{}, fmt.Errorf("invalid plugin ID %q", id)
	}
	manifest, metadata, err := i.installedMetadata(id)
	if err != nil {
		return InstallResult{}, err
	}
	if strings.TrimSpace(source) == "" {
		source = metadata.Source
	}
	result, err := i.install(ctx, source, id, true)
	if err != nil {
		return InstallResult{}, err
	}
	result.PreviousVersion = manifest.Version
	return result, nil
}

// UpgradeAll upgrades every Hash-managed bundle in deterministic ID order. It
// leaves developer links untouched and records them as skipped.
func (i *Installer) UpgradeAll(ctx context.Context, source string) (UpgradeAllResult, error) {
	ids, skipped, err := i.managedInstalledPluginIDs()
	if err != nil {
		return UpgradeAllResult{}, err
	}
	result := UpgradeAllResult{Skipped: skipped}
	for _, id := range ids {
		upgraded, err := i.Upgrade(ctx, id, source)
		if err != nil {
			result.Failures = append(result.Failures, UpgradeFailure{ID: id, Err: err})
			continue
		}
		result.Results = append(result.Results, upgraded)
	}
	return result, nil
}

// Uninstall removes only a plugin managed by Installer. Developer links are
// rejected so Hash never deletes a user's source checkout.
func (i *Installer) Uninstall(id string) error {
	if !pluginIDPattern.MatchString(id) {
		return fmt.Errorf("invalid plugin ID %q", id)
	}
	active := filepath.Join(i.pluginRoot, id)
	info, err := os.Lstat(active)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("plugin %q is not installed", id)
		}
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("plugin %q is not managed by Hash", id)
	}
	target, err := filepath.EvalSymlinks(active)
	if err != nil {
		return fmt.Errorf("resolve active plugin: %w", err)
	}
	managedIDRoot := filepath.Join(i.bundleRoot, id)
	if !pathWithin(managedIDRoot, target) {
		return fmt.Errorf("plugin %q is not managed by Hash", id)
	}
	if _, err := os.Stat(filepath.Join(target, installMetadataFilename)); err != nil {
		return fmt.Errorf("plugin %q is not managed by Hash", id)
	}
	if err := os.Remove(active); err != nil {
		return fmt.Errorf("remove active plugin: %w", err)
	}
	if err := os.RemoveAll(managedIDRoot); err != nil {
		return fmt.Errorf("remove managed bundles: %w", err)
	}
	return nil
}

func (i *Installer) install(ctx context.Context, source, expectedID string, replace bool) (InstallResult, error) {
	artifact, archive, actualChecksum, err := i.fetchArtifact(ctx, source, expectedID)
	if err != nil {
		return InstallResult{}, err
	}
	staging, manifest, err := i.stageArtifact(archive)
	if err != nil {
		return InstallResult{}, err
	}
	defer os.RemoveAll(staging) //nolint:errcheck // cleanup after rename is harmless
	if validationErr := validateArtifactManifest(manifest, artifact, expectedID); validationErr != nil {
		return InstallResult{}, validationErr
	}
	final, current, err := i.prepareDestination(manifest, replace)
	if err != nil {
		return InstallResult{}, err
	}
	if current != nil {
		manifest.Directory = current.Directory
		return newInstallResult(manifest, artifact, actualChecksum, false), nil
	}
	if metadataErr := writeInstallMetadata(staging, manifest, artifact, actualChecksum); metadataErr != nil {
		return InstallResult{}, metadataErr
	}
	if err = os.Rename(staging, final); err != nil {
		return InstallResult{}, fmt.Errorf("commit plugin bundle: %w", err)
	}
	if err = i.activate(manifest.ID, final, replace); err != nil {
		_ = os.RemoveAll(final)
		return InstallResult{}, err
	}
	manifest.Directory = final
	return newInstallResult(manifest, artifact, actualChecksum, true), nil
}

func (i *Installer) fetchArtifact(ctx context.Context, source, expectedID string) (artifact resolvedArtifact, archive []byte, checksum string, err error) {
	artifact, err = i.resolve(ctx, source, expectedID)
	if err != nil {
		return resolvedArtifact{}, nil, "", err
	}
	archive, err = i.download(ctx, artifact.url, maxArtifactBytes)
	if err != nil {
		return resolvedArtifact{}, nil, "", fmt.Errorf("download plugin artifact: %w", err)
	}
	digest := sha256.Sum256(archive)
	checksum = hex.EncodeToString(digest[:])
	if !strings.EqualFold(checksum, artifact.checksum) {
		return resolvedArtifact{}, nil, "", fmt.Errorf("artifact checksum mismatch: got %s, want %s", checksum, artifact.checksum)
	}
	return artifact, archive, checksum, nil
}

func (i *Installer) stageArtifact(archive []byte) (string, Manifest, error) {
	if err := os.MkdirAll(i.bundleRoot, 0o755); err != nil { //nolint:gosec // XDG data directory
		return "", Manifest{}, err
	}
	staging, err := os.MkdirTemp(i.bundleRoot, ".staging-")
	if err != nil {
		return "", Manifest{}, err
	}
	if err = extractPluginArchive(archive, staging); err != nil {
		_ = os.RemoveAll(staging)
		return "", Manifest{}, err
	}
	manifest, err := LoadManifest(staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		return "", Manifest{}, fmt.Errorf("invalid plugin bundle: %w", err)
	}
	return staging, manifest, nil
}

func validateArtifactManifest(manifest Manifest, artifact resolvedArtifact, expectedID string) error {
	if expectedID != "" && manifest.ID != expectedID {
		return fmt.Errorf("artifact plugin ID %q does not match installed plugin %q", manifest.ID, expectedID)
	}
	if artifact.expectedVersion != "" && manifest.Version != artifact.expectedVersion {
		return fmt.Errorf("manifest version %q does not match release %q", manifest.Version, artifact.expectedVersion)
	}
	if !installVersionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("version %q is not safe for managed installation", manifest.Version)
	}
	executableInfo, err := os.Stat(manifest.Executable())
	if err != nil || !executableInfo.Mode().IsRegular() || executableInfo.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("plugin entrypoint is not an executable regular file: %s", manifest.Entrypoint)
	}
	return nil
}

func (i *Installer) prepareDestination(manifest Manifest, replace bool) (string, *Manifest, error) {
	active := filepath.Join(i.pluginRoot, manifest.ID)
	if !replace {
		_, statErr := os.Lstat(active)
		if statErr == nil {
			return "", nil, fmt.Errorf("plugin %q is already installed; use hash plugin upgrade", manifest.ID)
		}
		if !os.IsNotExist(statErr) {
			return "", nil, statErr
		}
	}
	final := filepath.Join(i.bundleRoot, manifest.ID, manifest.Version)
	if replace {
		current, _, metadataErr := i.installedMetadata(manifest.ID)
		if metadataErr != nil {
			return "", nil, metadataErr
		}
		if current.Version == manifest.Version {
			return final, &current, nil
		}
	}
	_, statErr := os.Stat(final)
	if statErr == nil {
		return "", nil, fmt.Errorf("managed plugin version already exists: %s", final)
	}
	if !os.IsNotExist(statErr) {
		return "", nil, statErr
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(final), 0o755); mkdirErr != nil { //nolint:gosec // XDG data directory
		return "", nil, mkdirErr
	}
	return final, nil, nil
}

func writeInstallMetadata(staging string, manifest Manifest, artifact resolvedArtifact, checksum string) error {
	metadata := installMetadata{
		SchemaVersion: 1,
		ID:            manifest.ID,
		Version:       manifest.Version,
		Source:        artifact.source,
		ArtifactURL:   artifact.url,
		SHA256:        checksum,
		InstalledAt:   time.Now().UTC(),
	}
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	metadataBytes = append(metadataBytes, '\n')
	if err := os.WriteFile(filepath.Join(staging, installMetadataFilename), metadataBytes, 0o644); err != nil { //nolint:gosec // non-secret install metadata
		return err
	}
	return nil
}

func newInstallResult(manifest Manifest, artifact resolvedArtifact, checksum string, changed bool) InstallResult {
	return InstallResult{
		Manifest:    manifest,
		ID:          manifest.ID,
		Version:     manifest.Version,
		Source:      artifact.source,
		ArtifactURL: artifact.url,
		Checksum:    checksum,
		Changed:     changed,
	}
}

func (i *Installer) activate(id, target string, replace bool) error {
	if err := os.MkdirAll(i.pluginRoot, 0o755); err != nil { //nolint:gosec // XDG data directory
		return err
	}
	active := filepath.Join(i.pluginRoot, id)
	if replace {
		if _, _, err := i.installedMetadata(id); err != nil {
			return err
		}
	} else if _, err := os.Lstat(active); err == nil {
		return fmt.Errorf("plugin target already exists: %s", active)
	} else if !os.IsNotExist(err) {
		return err
	}
	placeholder, err := os.CreateTemp(i.pluginRoot, ".activate-")
	if err != nil {
		return err
	}
	temporary := placeholder.Name()
	if closeErr := placeholder.Close(); closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	if removeErr := os.Remove(temporary); removeErr != nil {
		return removeErr
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.Symlink(absTarget, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, active); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("activate plugin: %w", err)
	}
	return nil
}

func (i *Installer) installedMetadata(id string) (Manifest, installMetadata, error) {
	active := filepath.Join(i.pluginRoot, id)
	info, err := os.Lstat(active)
	if err != nil {
		return Manifest{}, installMetadata{}, fmt.Errorf("plugin %q is not installed", id)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return Manifest{}, installMetadata{}, fmt.Errorf("plugin %q is not managed by Hash", id)
	}
	target, err := filepath.EvalSymlinks(active)
	if err != nil || !pathWithin(filepath.Join(i.bundleRoot, id), target) {
		return Manifest{}, installMetadata{}, fmt.Errorf("plugin %q is not managed by Hash", id)
	}
	data, err := os.ReadFile(filepath.Join(target, installMetadataFilename))
	if err != nil {
		return Manifest{}, installMetadata{}, fmt.Errorf("plugin %q is not managed by Hash", id)
	}
	var metadata installMetadata
	decodeErr := json.Unmarshal(data, &metadata)
	if decodeErr != nil || metadata.SchemaVersion != 1 || metadata.ID != id {
		return Manifest{}, installMetadata{}, fmt.Errorf("plugin %q has invalid install metadata", id)
	}
	manifest, err := LoadManifest(target)
	if err != nil {
		return Manifest{}, installMetadata{}, err
	}
	return manifest, metadata, nil
}

func (i *Installer) managedInstalledPluginIDs() (managed, skipped []string, err error) {
	entries, err := os.ReadDir(i.pluginRoot)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range entries {
		id := entry.Name()
		if !pluginIDPattern.MatchString(id) {
			continue
		}
		if _, _, metadataErr := i.installedMetadata(id); metadataErr != nil {
			skipped = append(skipped, id)
			continue
		}
		managed = append(managed, id)
	}
	sort.Strings(managed)
	sort.Strings(skipped)
	return managed, skipped, nil
}

func (i *Installer) resolve(ctx context.Context, source, expectedID string) (resolvedArtifact, error) {
	if ref, ok, err := parseGitHubSource(source); err != nil {
		return resolvedArtifact{}, err
	} else if ok {
		return i.resolveGitHub(ctx, ref, expectedID)
	}
	u, err := url.Parse(strings.TrimSpace(source))
	if err != nil || u.Host == "" {
		return resolvedArtifact{}, fmt.Errorf("plugin source must be a GitHub repository or HTTPS artifact URL")
	}
	if err := i.validateRemoteURL(u); err != nil {
		return resolvedArtifact{}, err
	}
	fragment := u.Fragment
	u.Fragment = ""
	algorithm, checksum, ok := strings.Cut(fragment, "=")
	if !ok || algorithm != "sha256" || !validSHA256(checksum) {
		return resolvedArtifact{}, fmt.Errorf("direct artifact URL must include #sha256=<64-hex-digest>")
	}
	canonical := u.String() + "#sha256=" + strings.ToLower(checksum)
	return resolvedArtifact{url: u.String(), checksum: strings.ToLower(checksum), source: canonical}, nil
}

type githubSource struct {
	owner, repo, tag string
	canonical        string
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type githubAssetSelection struct {
	artifactName  string
	artifactURL   string
	checksumURL   string
	indexURL      string
	artifactCount int
	assetURLs     map[string]string
}

type githubReleaseIndex struct {
	Default string                         `json:"default"`
	Plugins map[string]githubPluginRelease `json:"plugins"`
}

type githubPluginRelease struct {
	Artifacts map[string]githubReleaseArtifact `json:"artifacts"`
}

type githubReleaseArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

func parseGitHubSource(source string) (githubSource, bool, error) {
	source = strings.TrimSpace(source)
	ref := githubSource{}
	if strings.HasPrefix(source, "github:") {
		reference := strings.TrimPrefix(source, "github:")
		if at := strings.LastIndex(reference, "@"); at >= 0 {
			ref.tag = reference[at+1:]
			if ref.tag == "" {
				return githubSource{}, true, fmt.Errorf("GitHub release tag must not be empty")
			}
			reference = reference[:at]
		}
		parts := strings.Split(strings.Trim(reference, "/"), "/")
		if len(parts) != 2 {
			return githubSource{}, true, fmt.Errorf("GitHub source must be github:owner/repo[@tag]")
		}
		ref.owner, ref.repo = parts[0], strings.TrimSuffix(parts[1], ".git")
	} else {
		u, err := url.Parse(source)
		if err != nil {
			return githubSource{}, false, fmt.Errorf("parse plugin source: %w", err)
		}
		if !strings.EqualFold(u.Hostname(), "github.com") {
			return githubSource{}, false, nil
		}
		if u.Scheme != "https" {
			return githubSource{}, true, fmt.Errorf("GitHub repository URL must use HTTPS")
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		switch {
		case len(parts) == 2:
			ref.owner, ref.repo = parts[0], strings.TrimSuffix(parts[1], ".git")
		case len(parts) == 5 && parts[2] == "releases" && parts[3] == "tag":
			ref.owner, ref.repo, ref.tag = parts[0], strings.TrimSuffix(parts[1], ".git"), parts[4]
		default:
			return githubSource{}, false, nil
		}
	}
	if !githubNamePattern.MatchString(ref.owner) || !githubNamePattern.MatchString(ref.repo) {
		return githubSource{}, true, fmt.Errorf("invalid GitHub repository reference")
	}
	ref.canonical = "github:" + ref.owner + "/" + ref.repo
	return ref, true, nil
}

func (i *Installer) resolveGitHub(ctx context.Context, ref githubSource, expectedID string) (resolvedArtifact, error) {
	release, err := i.fetchGitHubRelease(ctx, ref)
	if err != nil {
		return resolvedArtifact{}, fmt.Errorf("resolve GitHub release: %w", err)
	}
	selection, err := selectGitHubAssets(release, i.goos, i.goarch)
	if err != nil {
		return resolvedArtifact{}, err
	}
	checksums, err := i.download(ctx, selection.checksumURL, maxAPIResponseBytes)
	if err != nil {
		return resolvedArtifact{}, fmt.Errorf("download release checksums: %w", err)
	}
	artifactName, artifactURL := selection.artifactName, selection.artifactURL
	if selection.indexURL != "" {
		artifactName, artifactURL, err = i.resolveIndexedGitHubArtifact(ctx, release, selection, checksums, expectedID)
		if err != nil {
			return resolvedArtifact{}, err
		}
	}
	if artifactURL == "" {
		return resolvedArtifact{}, fmt.Errorf("release has no artifact for %s/%s", i.goos, i.goarch)
	}
	checksum := checksumForAsset(checksums, artifactName)
	if checksum == "" {
		return resolvedArtifact{}, fmt.Errorf("SHA256SUMS does not contain %s", artifactName)
	}
	version := strings.TrimPrefix(release.TagName, "v")
	return resolvedArtifact{url: artifactURL, checksum: checksum, source: ref.canonical, expectedVersion: version}, nil
}

func (i *Installer) indexedGitHubPluginIDs(ctx context.Context, source string) ([]string, string, error) {
	ref, ok, err := parseGitHubSource(source)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", fmt.Errorf("install --all requires a GitHub repository release with HASH_PLUGINS.json")
	}
	release, err := i.fetchGitHubRelease(ctx, ref)
	if err != nil {
		return nil, "", fmt.Errorf("resolve GitHub release: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return nil, "", fmt.Errorf("GitHub release has no tag")
	}
	selection, err := selectGitHubAssets(release, i.goos, i.goarch)
	if err != nil {
		return nil, "", err
	}
	if selection.indexURL == "" {
		return nil, "", fmt.Errorf("release does not publish HASH_PLUGINS.json; install one plugin without --all")
	}
	checksums, err := i.download(ctx, selection.checksumURL, maxAPIResponseBytes)
	if err != nil {
		return nil, "", fmt.Errorf("download release checksums: %w", err)
	}
	index, err := i.downloadGitHubReleaseIndex(ctx, selection.indexURL, checksums)
	if err != nil {
		return nil, "", err
	}
	ids := make([]string, 0, len(index.Plugins))
	for id := range index.Plugins {
		if !pluginIDPattern.MatchString(id) {
			return nil, "", fmt.Errorf("release index contains invalid plugin ID %q", id)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, "", fmt.Errorf("release index contains no plugins")
	}
	return ids, ref.canonical + "@" + release.TagName, nil
}

func (i *Installer) fetchGitHubRelease(ctx context.Context, ref githubSource) (githubRelease, error) {
	endpoint := strings.TrimRight(i.githubAPIBase, "/") + "/repos/" + url.PathEscape(ref.owner) + "/" + url.PathEscape(ref.repo) + "/releases/latest"
	if ref.tag != "" {
		endpoint = strings.TrimSuffix(endpoint, "/latest") + "/tags/" + url.PathEscape(ref.tag)
	}
	data, err := i.download(ctx, endpoint, maxAPIResponseBytes)
	if err != nil {
		return githubRelease{}, err
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return githubRelease{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	return release, nil
}

func selectGitHubAssets(release githubRelease, goos, goarch string) (githubAssetSelection, error) {
	selection := githubAssetSelection{assetURLs: map[string]string{}}
	suffix := "_" + goos + "_" + goarch + ".tar.gz"
	for _, asset := range release.Assets {
		selection.assetURLs[asset.Name] = asset.URL
		switch {
		case strings.HasSuffix(asset.Name, suffix):
			selection.artifactCount++
			selection.artifactName, selection.artifactURL = asset.Name, asset.URL
		case asset.Name == "SHA256SUMS":
			selection.checksumURL = asset.URL
		case asset.Name == "HASH_PLUGINS.json":
			selection.indexURL = asset.URL
		}
	}
	if selection.artifactCount > 1 && selection.indexURL == "" {
		return githubAssetSelection{}, fmt.Errorf("release has multiple artifacts for %s/%s", goos, goarch)
	}
	if selection.checksumURL == "" {
		return githubAssetSelection{}, fmt.Errorf("release has no SHA256SUMS asset")
	}
	return selection, nil
}

func (i *Installer) resolveIndexedGitHubArtifact(ctx context.Context, release githubRelease, selection githubAssetSelection, checksums []byte, expectedID string) (artifactName, artifactURL string, err error) {
	index, err := i.downloadGitHubReleaseIndex(ctx, selection.indexURL, checksums)
	if err != nil {
		return "", "", err
	}
	id, err := selectIndexedGitHubPlugin(index, expectedID)
	if err != nil {
		return "", "", err
	}
	entry, ok := index.Plugins[id]
	if !ok {
		return "", "", fmt.Errorf("release index has no plugin %q", id)
	}
	artifact, ok := entry.Artifacts[releasePlatformKey(i.goos, i.goarch)]
	if !ok {
		artifact, ok = entry.Artifacts[i.goos+"_"+i.goarch]
	}
	if !ok {
		return "", "", fmt.Errorf("release index has no safe artifact for %s/%s", i.goos, i.goarch)
	}
	artifactName = expandGitHubArtifactName(artifact.Name, release.TagName)
	if !safeGitHubArtifactName(artifactName) {
		return "", "", fmt.Errorf("release index has no safe artifact for %s/%s", i.goos, i.goarch)
	}
	artifactURL = selection.assetURLs[artifactName]
	if artifactURL == "" {
		return "", "", fmt.Errorf("release index artifact is incomplete")
	}
	return artifactName, artifactURL, nil
}

func (i *Installer) downloadGitHubReleaseIndex(ctx context.Context, indexURL string, checksums []byte) (githubReleaseIndex, error) {
	indexData, err := i.download(ctx, indexURL, maxAPIResponseBytes)
	if err != nil {
		return githubReleaseIndex{}, fmt.Errorf("download release index: %w", err)
	}
	indexChecksum := checksumForAsset(checksums, "HASH_PLUGINS.json")
	if indexChecksum == "" {
		return githubReleaseIndex{}, fmt.Errorf("SHA256SUMS does not contain HASH_PLUGINS.json")
	}
	digest := sha256.Sum256(indexData)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), indexChecksum) {
		return githubReleaseIndex{}, fmt.Errorf("release index checksum mismatch")
	}
	return decodeGitHubReleaseIndex(indexData)
}

func selectIndexedGitHubPlugin(index githubReleaseIndex, expectedID string) (string, error) {
	if expectedID != "" {
		if _, ok := index.Plugins[expectedID]; !ok {
			return "", fmt.Errorf("release index has no plugin %q", expectedID)
		}
		return expectedID, nil
	}
	ids := make([]string, 0, len(index.Plugins))
	for id := range index.Plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("release index contains no plugins")
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("release provides multiple plugins: %s; choose one with --id <plugin-id> or install all with --all", strings.Join(ids, ", "))
	}
}

func decodeGitHubReleaseIndex(data []byte) (githubReleaseIndex, error) {
	var index githubReleaseIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return githubReleaseIndex{}, fmt.Errorf("decode release index")
	}
	return index, nil
}

func releasePlatformKey(goos, goarch string) string {
	return goos + "/" + goarch
}

func expandGitHubArtifactName(name, tag string) string {
	version := strings.TrimPrefix(tag, "v")
	name = strings.ReplaceAll(name, "{{version}}", version)
	return strings.ReplaceAll(name, "{{ .Version }}", version)
}

func safeGitHubArtifactName(name string) bool {
	return name != "" && path.Base(name) == name && strings.HasSuffix(name, ".tar.gz")
}

func checksumForAsset(data []byte, name string) string {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name && validSHA256(fields[0]) {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (i *Installer) download(ctx context.Context, location string, limit int64) ([]byte, error) {
	u, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	if validationErr := i.validateRemoteURL(u); validationErr != nil {
		return nil, validationErr
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	req.Header.Set("User-Agent", "hash-plugin-installer/1")
	response, err := i.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

func (i *Installer) validateRemoteURL(u *url.URL) error {
	if u.Scheme == "https" {
		return nil
	}
	if i.allowHTTP && u.Scheme == "http" {
		return nil
	}
	return fmt.Errorf("plugin downloads require HTTPS")
}

func extractPluginArchive(archive []byte, destination string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("open plugin archive: %w", err)
	}
	defer func() { _ = gzipReader.Close() }()
	root, err := os.OpenRoot(destination)
	if err != nil {
		return fmt.Errorf("open extraction root: %w", err)
	}
	defer func() { _ = root.Close() }()
	tarReader := tar.NewReader(gzipReader)
	entries := 0
	var total int64
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read plugin archive: %w", err)
		}
		entries++
		if entries > maxArchiveEntries || header.Size < 0 {
			return fmt.Errorf("plugin archive exceeds safety limits")
		}
		total += header.Size
		if total > maxExtractedBytes {
			return fmt.Errorf("plugin archive exceeds extracted-size limit")
		}
		if err := extractArchiveEntry(tarReader, header, root); err != nil {
			return err
		}
	}
	return nil
}

func extractArchiveEntry(tarReader *tar.Reader, header *tar.Header, root *os.Root) error {
	clean := path.Clean(header.Name)
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("unsafe archive path %q", header.Name)
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return root.MkdirAll(clean, 0o755) //nolint:gosec // extraction is confined by os.Root
	case tar.TypeReg:
		return extractArchiveFile(tarReader, header, clean, root)
	case tar.TypeXHeader, tar.TypeXGlobalHeader:
		return nil
	default:
		return fmt.Errorf("unsupported archive entry %q", header.Name)
	}
}

func extractArchiveFile(tarReader *tar.Reader, header *tar.Header, name string, root *os.Root) error {
	if err := root.MkdirAll(path.Dir(name), 0o755); err != nil { //nolint:gosec // extraction is confined by os.Root
		return err
	}
	mode := os.FileMode(0o644)
	if header.Mode&0o111 != 0 {
		mode = 0o755
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) //nolint:gosec // extraction is confined by os.Root
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(file, tarReader, header.Size)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func pathWithin(root, candidate string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	if relativePathWithin(rootAbs, candidateAbs) {
		return true
	}
	rootResolved, rootErr := filepath.EvalSymlinks(rootAbs)
	candidateResolved, candidateErr := filepath.EvalSymlinks(candidateAbs)
	return rootErr == nil && candidateErr == nil && relativePathWithin(rootResolved, candidateResolved)
}

func relativePathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
