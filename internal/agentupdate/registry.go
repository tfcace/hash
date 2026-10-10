package agentupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultRegistryURL is the public npm registry.
const DefaultRegistryURL = "https://registry.npmjs.org"

// Fetcher answers "what is the latest published version of pkg".
type Fetcher interface {
	Latest(ctx context.Context, pkg string) (string, error)
}

// Registry fetches the latest version from an npm registry.
type Registry struct {
	BaseURL   string       // DefaultRegistryURL when empty
	Client    *http.Client // a 5s-timeout client when nil
	UserAgent string
}

// Latest GETs <BaseURL>/<pkg>/latest and returns its version field. The
// response is a few KB; the full packument would be megabytes.
func (r Registry) Latest(ctx context.Context, pkg string) (string, error) {
	base := r.BaseURL
	if base == "" {
		base = DefaultRegistryURL
	}
	base = strings.TrimRight(base, "/")
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+url.PathEscape(pkg)+"/latest", http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if r.UserAgent != "" {
		req.Header.Set("User-Agent", r.UserAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry: %s", resp.Status)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("registry: %w", err)
	}
	if body.Version == "" {
		return "", fmt.Errorf("registry: no version in response")
	}
	return body.Version, nil
}
