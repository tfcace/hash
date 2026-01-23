#!/usr/bin/env bash
set -euo pipefail

# Hash build script with version injection
# Usage:
#   ./scripts/build.sh            # Build to ./hash
#   ./scripts/build.sh --install  # Build to /usr/local/bin/hash

VERSION="${VERSION:-0.1.0}"
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
JJ_CHANGE=$(jj log -r @ --no-graph -T 'change_id.shortest()' 2>/dev/null || echo "unknown")
BUILD_DATE=$(date -u +%Y-%m-%d)

LDFLAGS="-X github.com/tfcace/hash/internal/version.Version=${VERSION}"
LDFLAGS="${LDFLAGS} -X github.com/tfcace/hash/internal/version.GitCommit=${GIT_COMMIT}"
LDFLAGS="${LDFLAGS} -X github.com/tfcace/hash/internal/version.JjChange=${JJ_CHANGE}"
LDFLAGS="${LDFLAGS} -X github.com/tfcace/hash/internal/version.BuildDate=${BUILD_DATE}"

OUTPUT="./hash"
if [[ "${1:-}" == "--install" ]]; then
    OUTPUT="/usr/local/bin/hash"
fi

echo "Building hash ${VERSION} (jj:${JJ_CHANGE} git:${GIT_COMMIT} ${BUILD_DATE})"
go build -ldflags "${LDFLAGS}" -o "${OUTPUT}" ./cmd/hash

echo "Built: ${OUTPUT}"
"${OUTPUT}" --version
