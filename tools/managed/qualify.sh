#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 OpenCodeReview fork contributors
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: tools/managed/qualify.sh <absolute-binary-path> <receipt-path>" >&2
  exit 2
fi
root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
cd "$root"
export GOTOOLCHAIN=local GOENV=off GOWORK=off GO111MODULE=on GOFIPS140=off LC_ALL=C
source tools/managed/release.env
[[ "$GO_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
if [ "$(go version | awk '{print $3}')" != "go${GO_VERSION}" ]; then
  echo "qualify.sh: Go ${GO_VERSION} is required" >&2
  exit 1
fi
exec go run -mod=readonly ./tools/managed/qualification "$1" "$2"
