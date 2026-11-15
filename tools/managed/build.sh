#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 OpenCodeReview fork contributors

set -euo pipefail

if (( $# > 1 )); then
  echo "Usage: tools/managed/build.sh [output-directory]" >&2
  exit 2
fi

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

if [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
  echo "Build requires a clean committed source tree, including untracked files." >&2
  exit 1
fi

source_commit="$(git rev-parse --verify HEAD)"
source_tree="$(git rev-parse "${source_commit}^{tree}")"

output_directory="${1:-dist/managed}"
if [[ -e "$output_directory" || -L "$output_directory" ]]; then
  echo "Refusing to overwrite output directory: ${output_directory}" >&2
  exit 1
fi
mkdir -p -- "$(dirname -- "$output_directory")"
output_parent="$(cd -- "$(dirname -- "$output_directory")" && pwd -P)"
output_directory="${output_parent}/$(basename -- "$output_directory")"
if [[ "$output_directory" == "${repo_root}/"* ]] && ! git check-ignore --quiet -- "$output_directory"; then
  echo "Output must be outside the checkout or inside a Git-ignored directory." >&2
  exit 1
fi
staging_directory="$(mktemp -d "${output_parent}/.managed-build.XXXXXX")"
source_directory="$(mktemp -d)"
trap 'rm -rf -- "$source_directory" "$staging_directory"' EXIT

git archive --format=tar "$source_commit" | tar -xf - -C "$source_directory"
source "${source_directory}/tools/managed/release.env"
[[ "$GO_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$RELEASE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+-rr\.[0-9]+$ ]]
[[ "$UPSTREAM_COMMIT" =~ ^[0-9a-f]{40}$ ]]
git merge-base --is-ancestor "$UPSTREAM_COMMIT" "$source_commit"

go_binary="$(command -v go)"
go_binary="$(cd -- "$(dirname -- "$go_binary")" && pwd -P)/$(basename -- "$go_binary")"
actual_version="$(GOENV=off GOTOOLCHAIN=local GOWORK=off "$go_binary" version)"
if [[ "$actual_version" != "go version go${GO_VERSION} "* ]]; then
  echo "Expected Go ${GO_VERSION}; observed: ${actual_version}" >&2
  exit 1
fi

binary_name=opencodereview-linux-amd64
(
  cd "$source_directory"
  env GOENV=off GOTOOLCHAIN=local GOWORK=off GO111MODULE=on GOFIPS140=off \
    GOFLAGS= GOEXPERIMENT= GOAMD64=v1 \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 LC_ALL=C \
    "$go_binary" build -mod=readonly -trimpath -buildvcs=false \
    -ldflags "-X=main.Version=${RELEASE_VERSION}" \
    -o "${staging_directory}/${binary_name}" ./cmd/opencodereview
)

binary_sha256="$(sha256sum "${staging_directory}/${binary_name}" | cut -d ' ' -f 1)"
binary_size="$(wc -c < "${staging_directory}/${binary_name}" | tr -d '[:space:]')"
printf '%s  %s\n' "$binary_sha256" "$binary_name" > "${staging_directory}/${binary_name}.sha256"
cp -- "${source_directory}/LICENSE" "${staging_directory}/LICENSE"
cp -- "${source_directory}/NOTICE" "${staging_directory}/NOTICE"
cp -- "${source_directory}/tools/managed/provenance/ReviewRouter-LICENSE" "${staging_directory}/ReviewRouter-LICENSE"
license_sha256="$(sha256sum "${staging_directory}/LICENSE" | cut -d ' ' -f 1)"
notice_sha256="$(sha256sum "${staging_directory}/NOTICE" | cut -d ' ' -f 1)"
reviewrouter_license_sha256="$(sha256sum "${staging_directory}/ReviewRouter-LICENSE" | cut -d ' ' -f 1)"
cat > "${staging_directory}/MANIFEST" <<EOF
manifest_schema=open-code-review.build/v1
repository=agent-teams-ai/open-code-review
source_commit=${source_commit}
source_tree=${source_tree}
upstream_repository=${UPSTREAM_REPOSITORY}
upstream_commit=${UPSTREAM_COMMIT}
toolchain=go${GO_VERSION}
goos=linux
goarch=amd64
goamd64=v1
gofips140=off
gowork=off
cgo_enabled=0
build_flags=-mod=readonly -trimpath -buildvcs=false -ldflags -X=main.Version=${RELEASE_VERSION}
release_version=${RELEASE_VERSION}
outcome_schema_version=${OUTCOME_SCHEMA_VERSION}
binary_name=${binary_name}
binary_sha256=${binary_sha256}
binary_size_bytes=${binary_size}
license_sha256=${license_sha256}
notice_sha256=${notice_sha256}
reviewrouter_license_sha256=${reviewrouter_license_sha256}
EOF

if [[ "$(git rev-parse HEAD)" != "$source_commit" || -n "$(git status --porcelain --untracked-files=normal)" ]]; then
  echo "Source checkout changed during the build; no candidate published." >&2
  exit 1
fi
mv -T -- "$staging_directory" "$output_directory"
printf 'Built %s from %s\nSHA-256: %s\n' \
  "${output_directory}/${binary_name}" "$source_commit" "$binary_sha256"
