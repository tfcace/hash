#!/usr/bin/env bash
set -euo pipefail

# Hash build script with version injection
# Usage:
#   ./scripts/build.sh            # Build to ./hash
#   ./scripts/build.sh --install  # Build to /usr/local/bin/hash

# Read version from release-please manifest, fallback to 0.1.0
if [[ -f ".release-please-manifest.json" ]]; then
    VERSION="${VERSION:-$(jq -r '."."' .release-please-manifest.json 2>/dev/null || echo "0.1.0")}"
else
    VERSION="${VERSION:-0.1.0}"
fi
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
JJ_CHANGE=$(jj log -r @ --no-graph -T 'change_id.shortest()' 2>/dev/null || echo "unknown")
BUILD_DATE=$(date -u +%Y-%m-%d)

LDFLAGS="-X github.com/tfcace/hash/internal/version.Version=${VERSION}"
LDFLAGS="${LDFLAGS} -X github.com/tfcace/hash/internal/version.GitCommit=${GIT_COMMIT}"
LDFLAGS="${LDFLAGS} -X github.com/tfcace/hash/internal/version.JjChange=${JJ_CHANGE}"
LDFLAGS="${LDFLAGS} -X github.com/tfcace/hash/internal/version.BuildDate=${BUILD_DATE}"

INSTALL=false
if [[ "${1:-}" == "--install" ]]; then
    INSTALL=true
fi

echo "Building hash ${VERSION} (jj:${JJ_CHANGE} git:${GIT_COMMIT} ${BUILD_DATE})"
go build -ldflags "${LDFLAGS}" -o "./hash" ./cmd/hash

if [[ "$INSTALL" == "true" ]]; then
    echo "Installing to /usr/local/bin/hash (requires sudo)"
    sudo cp ./hash /usr/local/bin/hash
    # Re-sign after copy to fix code signature invalidation on macOS
    if [[ "$(uname)" == "Darwin" ]]; then
        sudo codesign --force --sign - /usr/local/bin/hash
    fi
    echo "Installed: /usr/local/bin/hash"
    /usr/local/bin/hash --version
else
    echo "Built: ./hash"
    ./hash --version
fi
