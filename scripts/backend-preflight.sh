#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_DIR="${ROOT_DIR}/server"

if [[ ! -d "${SERVER_DIR}" ]]; then
  echo "ERROR: server directory not found: ${SERVER_DIR}" >&2
  exit 1
fi

cd "${SERVER_DIR}"

echo
echo "========================================"
echo " Multica Backend Preflight"
echo "========================================"
echo "Directory : ${SERVER_DIR}"
echo "Go        : $(go version)"
echo

run_step() {
  local name="$1"
  shift

  echo
  echo "----------------------------------------"
  echo ">> ${name}"
  echo "----------------------------------------"

  if "$@"; then
    echo "✓ ${name}"
  else
    local exit_code=$?
    echo
    echo "✗ ${name} FAILED (exit=${exit_code})" >&2
    exit "${exit_code}"
  fi
}

# 1. Module/package compile check.
#
# `go test` compiles packages before running tests, so compile-time type errors
# such as incompatible json.RawMessage / map[string]any values are caught here.
run_step \
  "Compile all packages + run tests" \
  go test ./...

# 2. Catch suspicious constructs that still compile.
run_step \
  "Go vet" \
  go vet ./...

# 3. Build exactly the same binaries produced by Dockerfile.
#
# Output goes to a temporary directory so the repository stays clean.
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "${BUILD_DIR}"' EXIT

VERSION="${VERSION:-preflight}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
DATE="${DATE:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}"

run_step \
  "Build server" \
  env CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o "${BUILD_DIR}/server" \
    ./cmd/server

run_step \
  "Build multica CLI" \
  env CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o "${BUILD_DIR}/multica" \
    ./cmd/multica

run_step \
  "Build migrate" \
  env CGO_ENABLED=0 go build \
    -ldflags "-s -w" \
    -o "${BUILD_DIR}/migrate" \
    ./cmd/migrate

echo
echo "========================================"
echo "✓ BACKEND PREFLIGHT PASSED"
echo "========================================"
echo "Docker backend build can now be started."