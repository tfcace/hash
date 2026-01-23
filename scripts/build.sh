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

LDFLAGS="-X main.version=${VERSION}"
LDFLAGS="${LDFLAGS} -X main.gitCommit=${GIT_COMMIT}"
LDFLAGS="${LDFLAGS} -X main.jjChange=${JJ_CHANGE}"
LDFLAGS="${LDFLAGS} -X main.buildDate=${BUILD_DATE}"

OUTPUT="./hash"
if [[ "${1:-}" == "--install" ]]; then
    OUTPUT="/usr/local/bin/hash"
fi

echo "Building hash ${VERSION} (jj:${JJ_CHANGE} git:${GIT_COMMIT} ${BUILD_DATE})"
go build -ldflags "${LDFLAGS}" -o "${OUTPUT}" ./cmd/hash

echo "Built: ${OUTPUT}"
"${OUTPUT}" --version
