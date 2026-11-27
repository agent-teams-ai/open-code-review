#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 OpenCodeReview fork contributors

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "$0")/../.." && pwd -P)"
workflow="$repo_root/.github/workflows/managed-release.yml"
if (( $# == 1 )); then
  workflow="$1"
elif (( $# > 1 )); then
  echo "Usage: $0 [workflow-path]" >&2
  exit 2
fi

fixture_root="$(mktemp -d)"
trap 'rm -rf -- "$fixture_root"' EXIT
mkdir "$fixture_root/bin"
awk '
  /^      - name: Publish once, after verifying remote draft assets$/ { step = 1; next }
  step && /^        run: \|$/ { script = 1; next }
  script && /^          / { print substr($0, 11); next }
  script && /^$/ { print; next }
  script { exit }
' "$workflow" > "$fixture_root/publish.sh"
[[ -s "$fixture_root/publish.sh" ]]

cat > "$fixture_root/bin/gh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FIXTURE_STATE/requests.log"

not_found() {
  if [[ "$FAKE_CASE" == preflight_error ]]; then
    printf 'HTTP/2.0 403 Forbidden\n'
  else
    printf 'HTTP/2.0 404 Not Found\n'
  fi
  exit 1
}

case "$1" in
  api)
    shift
    if [[ "$1" == --include ]]; then shift; fi
    endpoint="$1"
    shift
    query='.'
    if (( $# > 0 )); then
      [[ "$1" == --jq && $# == 2 ]]
      query="$2"
    fi
    case "$endpoint" in
      */git/ref/heads/main)
        if [[ "$FAKE_CASE" == source_changed ]]; then
          printf '%040d\n' 0
        else
          printf '%s\n' "$EXPECTED_SOURCE_SHA"
        fi
        ;;
      */git/ref/tags/*)
        if [[ "$FAKE_CASE" == existing_tag || -f "$FIXTURE_STATE/published" ]]; then
          printf '%s\n' "$EXPECTED_SOURCE_SHA"
        else
          not_found
        fi
        ;;
      */releases/tags/*)
        # REST by-tag lookup cannot resolve a pending draft.
        if [[ -f "$FIXTURE_STATE/published" ]]; then
          printf '{"draft":false}\n' | jq "$query"
        else
          not_found
        fi
        ;;
      */releases/42)
        jq "$query" "$FIXTURE_STATE/remote-metadata.json"
        ;;
      *)
        echo "Unexpected API request: $endpoint" >&2
        exit 90
        ;;
    esac
    ;;
  release)
    case "$2" in
      create)
        touch "$FIXTURE_STATE/created"
        ;;
      download)
        [[ "$4" == --dir && -f "$FIXTURE_STATE/created" ]]
        mkdir "$5"
        cp candidate/* "$5/"
        if [[ "$FAKE_CASE" == corrupt_asset ]]; then
          printf 'corrupted\n' >> "$5/MANIFEST"
        fi
        ;;
      view)
        [[ "$4" == --json && "$5" == databaseId && "$6" == --jq && "$7" == .databaseId ]]
        if [[ "$FAKE_CASE" == invalid_id ]]; then printf 'null\n'; else printf '42\n'; fi
        ;;
      edit)
        [[ "$4" == --draft=false ]]
        touch "$FIXTURE_STATE/published"
        ;;
      *)
        echo "Unexpected release command: $2" >&2
        exit 91
        ;;
    esac
    ;;
  *)
    echo "Unexpected gh command: $1" >&2
    exit 92
    ;;
esac
SH
chmod +x "$fixture_root/bin/gh"

source_sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
for case_name in valid invalid_id wrong_source wrong_tag not_draft missing_asset extra_asset corrupt_asset existing_tag preflight_error source_changed; do
  case_dir="$fixture_root/$case_name"
  mkdir -p "$case_dir/candidate"
  for asset in LICENSE NOTICE ReviewRouter-LICENSE MANIFEST opencodereview-linux-amd64 opencodereview-linux-amd64.sha256 qualification.json; do
    printf '%s fixture\n' "$asset" > "$case_dir/candidate/$asset"
  done
  jq -n --arg source "$source_sha" '{
    draft: true,
    tag_name: "ocr-v1.14.0-rr.1",
    target_commitish: $source,
    assets: ["LICENSE", "NOTICE", "ReviewRouter-LICENSE", "MANIFEST",
      "opencodereview-linux-amd64", "opencodereview-linux-amd64.sha256",
      "qualification.json"] | map({name: .})
  }' > "$case_dir/remote-metadata.json"
  mutation='.'
  case "$case_name" in
    wrong_source) mutation='.target_commitish = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"' ;;
    wrong_tag) mutation='.tag_name = "ocr-v9.9.9-rr.1"' ;;
    not_draft) mutation='.draft = false' ;;
    missing_asset) mutation='.assets |= .[1:]' ;;
    extra_asset) mutation='.assets += [{name: "unexpected"}]' ;;
  esac
  jq "$mutation" "$case_dir/remote-metadata.json" > "$case_dir/metadata.json"
  mv "$case_dir/metadata.json" "$case_dir/remote-metadata.json"

  if (
    cd "$case_dir"
    PATH="$fixture_root/bin:$PATH" \
      FAKE_CASE="$case_name" FIXTURE_STATE="$case_dir" RUNNER_TEMP="$case_dir" \
      GH_TOKEN=fixture-token GH_REPO=fixture/repository \
      EXPECTED_SOURCE_SHA="$source_sha" RELEASE_VERSION=1.14.0-rr.1 \
      BINARY_SHA256=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
      bash "$fixture_root/publish.sh"
  ) > "$case_dir/output.log" 2>&1; then
    status=0
  else
    status=$?
  fi
  if [[ "$case_name" == valid ]]; then
    if [[ "$status" != 0 || ! -f "$case_dir/published" ]]; then
      cat "$case_dir/requests.log" "$case_dir/output.log" >&2
      echo "Valid pending draft was not published." >&2
      exit 1
    fi
  elif [[ "$status" == 0 || -f "$case_dir/published" ]]; then
    cat "$case_dir/output.log" >&2
    echo "Invalid release state was published: $case_name" >&2
    exit 1
  fi
  case "$case_name" in
    existing_tag|preflight_error|source_changed)
      [[ ! -f "$case_dir/created" ]] || { echo "Preflight failure wrote a draft: $case_name" >&2; exit 1; }
      ;;
  esac
  printf 'PASS managed release protocol: %s\n' "$case_name"
done
