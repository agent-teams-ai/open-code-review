#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 OpenCodeReview fork contributors

# Preserve the original per-file licenses; these five imports had no headers
# and therefore retain the source repository's MIT license.
license_policy() {
  LICENSE_IDENTIFIER=Apache-2.0
  LICENSE_OWNER='alibaba/open-code-review Contributors'
  case "$1" in
    internal/agent/reviewrouter_skills_test.go|\
    internal/agent/template_opacity_test.go|\
    internal/config/rules/reviewrouter_skills_test.go|\
    internal/llm/serialize.go|\
    internal/llm/serialize_precanceled_test.go)
      LICENSE_IDENTIFIER=MIT
      LICENSE_OWNER=iliya
      ;;
    tools/managed/*|scripts/license-policy.sh)
      LICENSE_OWNER='OpenCodeReview fork contributors'
      ;;
  esac
}
