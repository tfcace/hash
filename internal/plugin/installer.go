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
	artifact, err := i.resolve(ctx, source)
	if err != nil {
		return InstallResult{}, err
	}
	archive, err := i.download(ctx, artifact.url, maxArtifactBytes)
	if err != nil {
		return InstallResult{}, fmt.Errorf("download plugin artifact: %w", err)
	}
	digest := sha256.Sum256(archive)
	actualChecksum := hex.EncodeToString(digest[:])
	if !strings.EqualFold(actualChecksum, artifact.checksum) {
		return InstallResult{}, fmt.Errorf("artifact checksum mismatch: got %s, want %s", actualChecksum, artifact.checksum)
	}

	if err := os.MkdirAll(i.bundleRoot, 0o755); err != nil { //nolint:gosec // XDG data directory
		return InstallResult{}, err
	}
	staging, err := os.MkdirTemp(i.bundleRoot, ".staging-")
	if err != nil {
		return InstallResult{}, err
	}
	defer os.RemoveAll(staging) //nolint:errcheck // cleanup after rename is harmless
	if err := extractPluginArchive(archive, staging); err != nil {
		return InstallResult{}, err
	}
	manifest, err := LoadManifest(staging)
	if err != nil {
		return InstallResult{}, fmt.Errorf("invalid plugin bundle: %w", err)
	}
	if expectedID != "" && manifest.ID != expectedID {
		return InstallResult{}, fmt.Errorf("artifact plugin ID %q does not match installed plugin %q", manifest.ID, expectedID)
	}
	if artifact.expectedVersion != "" && manifest.Version != artifact.expectedVersion {
		return InstallResult{}, fmt.Errorf("manifest version %q does not match release %q", manifest.Version, artifact.expectedVersion)
	}
	if !installVersionPattern.MatchString(manifest.Version) {
		return InstallResult{}, fmt.Errorf("version %q is not safe for managed installation", manifest.Version)
	}
	executableInfo, err := os.Stat(manifest.Executable())
	if err != nil || !executableInfo.Mode().IsRegular() || executableInfo.Mode().Perm()&0o111 == 0 {
		return InstallResult{}, fmt.Errorf("plugin entrypoint is not an executable regular file: %s", manifest.Entrypoint)
	}

	active := filepath.Join(i.pluginRoot, manifest.ID)
	if !replace {
		if _, err := os.Lstat(active); err == nil {
			return InstallResult{}, fmt.Errorf("plugin %q is already installed; use hash plugin upgrade", manifest.ID)
		} else if !os.IsNotExist(err) {
			return InstallResult{}, err
		}
	}
	final := filepath.Join(i.bundleRoot, manifest.ID, manifest.Version)
	if replace {
		if current, _, metadataErr := i.installedMetadata(manifest.ID); metadataErr != nil {
			return InstallResult{}, metadataErr
		} else if current.Version == manifest.Version {
			manifest.Directory = current.Directory
			return InstallResult{Manifest: manifest, ID: manifest.ID, Version: manifest.Version, Source: artifact.source, ArtifactURL: artifact.url, Checksum: actualChecksum, Changed: false}, nil
		}
	}
	if _, err := os.Stat(final); err == nil {
		return InstallResult{}, fmt.Errorf("managed plugin version already exists: %s", final)
	} else if !os.IsNotExist(err) {
		return InstallResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil { //nolint:gosec // XDG data directory
		return InstallResult{}, err
	}
	metadata := installMetadata{
		SchemaVersion: 1,
		ID:            manifest.ID,
		Version:       manifest.Version,
		Source:        artifact.source,
		ArtifactURL:   artifact.url,
		SHA256:        actualChecksum,
		InstalledAt:   time.Now().UTC(),
	}
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return InstallResult{}, err
	}
	metadataBytes = append(metadataBytes, '\n')
	if err := os.WriteFile(filepath.Join(staging, installMetadataFilename), metadataBytes, 0o644); err != nil { //nolint:gosec // non-secret install metadata
		return InstallResult{}, err
	}
	if err := os.Rename(staging, final); err != nil {
		return InstallResult{}, fmt.Errorf("commit plugin bundle: %w", err)
	}
	if err := i.activate(manifest.ID, final, replace); err != nil {
		_ = os.RemoveAll(final)
		return InstallResult{}, err
	}
	manifest.Directory = final
	return InstallResult{Manifest: manifest, ID: manifest.ID, Version: manifest.Version, Source: artifact.source, ArtifactURL: artifact.url, Checksum: actualChecksum, Changed: true}, nil
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
	if err := placeholder.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Remove(temporary); err != nil {
		return err
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
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.SchemaVersion != 1 || metadata.ID != id {
		return Manifest{}, installMetadata{}, fmt.Errorf("plugin %q has invalid install metadata", id)
	}
	manifest, err := LoadManifest(target)
	if err != nil {
		return Manifest{}, installMetadata{}, err
	}
	return manifest, metadata, nil
}

func (i *Installer) resolve(ctx context.Context, source string) (resolvedArtifact, error) {
	if ref, ok, err := parseGitHubSource(source); err != nil {
		return resolvedArtifact{}, err
	} else if ok {
		return i.resolveGitHub(ctx, ref)
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
		if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
			return githubSource{}, false, nil
		}
		if u.Scheme != "https" {
			return githubSource{}, true, fmt.Errorf("GitHub repository URL must use HTTPS")
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 2 {
			ref.owner, ref.repo = parts[0], strings.TrimSuffix(parts[1], ".git")
		} else if len(parts) == 5 && parts[2] == "releases" && parts[3] == "tag" {
			ref.owner, ref.repo, ref.tag = parts[0], strings.TrimSuffix(parts[1], ".git"), parts[4]
		} else {
			return githubSource{}, false, nil
		}
	}
	if !githubNamePattern.MatchString(ref.owner) || !githubNamePattern.MatchString(ref.repo) {
		return githubSource{}, true, fmt.Errorf("invalid GitHub repository reference")
	}
	ref.canonical = "github:" + ref.owner + "/" + ref.repo
	return ref, true, nil
}

func (i *Installer) resolveGitHub(ctx context.Context, ref githubSource) (resolvedArtifact, error) {
	endpoint := strings.TrimRight(i.githubAPIBase, "/") + "/repos/" + url.PathEscape(ref.owner) + "/" + url.PathEscape(ref.repo) + "/releases/latest"
	if ref.tag != "" {
		endpoint = strings.TrimSuffix(endpoint, "/latest") + "/tags/" + url.PathEscape(ref.tag)
	}
	data, err := i.download(ctx, endpoint, maxAPIResponseBytes)
	if err != nil {
		return resolvedArtifact{}, fmt.Errorf("resolve GitHub release: %w", err)
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return resolvedArtifact{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	suffix := "_" + i.goos + "_" + i.goarch + ".tar.gz"
	var artifactName, artifactURL, checksumURL string
	for _, asset := range release.Assets {
		switch {
		case strings.HasSuffix(asset.Name, suffix):
			if artifactURL != "" {
				return resolvedArtifact{}, fmt.Errorf("release has multiple artifacts for %s/%s", i.goos, i.goarch)
			}
			artifactName, artifactURL = asset.Name, asset.URL
		case asset.Name == "SHA256SUMS":
			checksumURL = asset.URL
		}
	}
	if artifactURL == "" {
		return resolvedArtifact{}, fmt.Errorf("release has no artifact for %s/%s", i.goos, i.goarch)
	}
	if checksumURL == "" {
		return resolvedArtifact{}, fmt.Errorf("release has no SHA256SUMS asset")
	}
	checksums, err := i.download(ctx, checksumURL, maxAPIResponseBytes)
	if err != nil {
		return resolvedArtifact{}, fmt.Errorf("download release checksums: %w", err)
	}
	checksum := checksumForAsset(checksums, artifactName)
	if checksum == "" {
		return resolvedArtifact{}, fmt.Errorf("SHA256SUMS does not contain %s", artifactName)
	}
	version := strings.TrimPrefix(release.TagName, "v")
	return resolvedArtifact{url: artifactURL, checksum: checksum, source: ref.canonical, expectedVersion: version}, nil
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
	if err := i.validateRemoteURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
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
	defer gzipReader.Close()
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
		clean := path.Clean(header.Name)
		if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		if !pathWithin(destination, target) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil { //nolint:gosec // extracted bundle directory
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // extracted bundle directory
				return err
			}
			mode := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) //nolint:gosec // archive permissions are clamped
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, tarReader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			continue
		default:
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
	return nil
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
