# Maintaining the managed OpenCodeReview fork

This source tree carries the native OpenCodeReview CLI used by ReviewRouter
and selected for the Task Verification experiment. Its organization repository
destination is `agent-teams-ai/open-code-review`. It preserves the history of
[alibaba/open-code-review](https://github.com/alibaba/open-code-review) and
imports the managed changes that were previously applied during ReviewRouter
builds.

The checked-out Go source is the maintained implementation. Future fixes belong
in ordinary source commits here. Do not maintain a second, independently edited
patch stack in ReviewRouter.

The recovered tracked-inventory work and its stale patch-based delivery plan
are reconciled in [the managed inventory plan](managed-tracked-inventory.md).

## Scope and ownership

The fork owns the native executable, its managed CLI behavior and outcome
contract, tests, reproducible build inputs and release artifacts. It remains a
CLI that can be run outside ReviewRouter; it does not require a new hosted
service. The Go module path remains `github.com/alibaba/open-code-review` during
this extraction to preserve internal imports and avoid an unrelated module
migration.

ReviewRouter owns its authorization, process supervision, Gateway integration,
finding normalization, publication rules and runtime admission. Those product
policies stay in ReviewRouter. Task Verification owns its experiment fixtures,
scoring and evaluation protocol. It can qualify this executable against a local
fake provider without granting it credentials or making live model calls.

The existing `reviewrouter.ocr-outcome/v1` schema and managed CLI behavior remain
unchanged by the source extraction. A later schema change needs a documented
consumer migration and qualification. Copying the source into an organization
repository does not activate a new ReviewRouter runtime.

The upstream README, package installation instructions, website, editor
extensions and benchmark claims are retained as upstream documentation. They do
not describe a separately qualified fork release or measurements made by this
fork. The managed Linux/amd64 artifact is the initial consumer target.

Run the managed native executable directly. The retained upstream npm package,
`bin/ocr.js` and npm installers select the upstream distribution; its Node
launcher can update that upstream package without the managed patches. They are
not installation or launch routes for this fork. The native Go executable has
no self-updater at this baseline.

## Migration baseline

| Input | Frozen identity |
| --- | --- |
| Upstream source | `alibaba/open-code-review@8eb83340a487b1150af00701b94f01af97e4b4d6` |
| Upstream version | `1.13.1` |
| Imported patch source | `777genius/review-router@1112a0f1de568f9fa927316ab68cef54fbcd4653`, `tools/ocr/patches/` |
| Patch count | 12, applied in the original filename order |
| Original patch-set SHA-256 | `1580f482c7dba27c3a107d0e5d69bdd681cccc2b67d1e8ccd4af45d40b383d5b` |
| Exact imported source commit | `31f2043c7a8663958b41cffa91ec4a9f17b9b1ea` |
| Exact imported source tree | `801ae83711ce66353bb60871463555db81102d16` |
| Approved binary version | `1.13.1-rr.6` |
| Approved toolchain | `go1.26.10` |
| Existing outcome schema | `reviewrouter.ocr-outcome/v1` |

The two patch filenames beginning with `0011-` are intentional. Both belong to
the approved stack. Imported commits must retain their source provenance and
attribution; do not flatten the upstream history or rewrite published commits.

The import record is
[`tools/managed/provenance/review-router-import.json`](../tools/managed/provenance/review-router-import.json).
The original manifest and
[ReviewRouter's MIT license](../tools/managed/provenance/ReviewRouter-LICENSE)
are retained alongside it. These files record the historical import; they are
not another editable implementation of the managed patch stack.

The upstream Apache-2.0 license remains in [LICENSE](../LICENSE). Preserve the
licenses, copyright notices and provenance of imported ReviewRouter material.
The Task Verification agreement about research documents does not relicense
this fork or upstream documentation.

### Existing approved artifact

The original approved release remains available from
[ReviewRouter's `ocr-v1.13.1-rr.6` release](https://github.com/777genius/review-router/releases/tag/ocr-v1.13.1-rr.6):

| Field | Value |
| --- | --- |
| Asset | `opencodereview-linux-amd64` |
| Size | 80,513,363 bytes |
| SHA-256 | `9dd5f115747d24100f0767133a3288d268f3bc399675f5b826e7a867d101ee5c` |
| Original release build commit | `777genius/review-router@49afbae9debba7e30251637c4621d164595dc2c5` |

Keep this release available for frozen experiments. An organization-hosted copy
of this artifact must contain those exact bytes and retain its original build
provenance. A fresh build with a different digest is a new artifact, even if its
source is equivalent; it must not replace the approved binary under the old
identity.

The imported source was independently replayed from the pinned upstream and all
12 original patches; all 1,132 file hashes matched. Building that exact imported
commit with Go 1.26.10 and the original `1.13.1-rr.6` version flag reproduced the
approved binary byte for byte, including the size and SHA-256 above. These
observations are recorded in the import provenance. They establish preservation
of the historical implementation before subsequent fork packaging changes.

The experiment's parser input is a separate consumer pin:
`777genius/review-router@7a079c382d8e98b426ee90a6b3764be2a21b4c49`.
It identifies the Action's outcome parser and finding normalization modules,
not the source commit that produced the native binary. Preserve both identities
in experiment evidence.

## Build and qualification

`tools/managed/build.sh` is the fork's build entrypoint and
`tools/managed/qualify.sh` is its model-free qualification entrypoint. On
Linux/amd64 with Go 1.26.11 on `PATH`, run from a clean, committed checkout:

```sh
tools/managed/build.sh
tools/managed/qualify.sh \
  "$PWD/dist/managed/opencodereview-linux-amd64" \
  "$PWD/dist/managed/qualification.json"
```

The builder accepts an optional output-directory argument, archives the exact
commit and refuses to overwrite an existing output directory. Qualification
requires an absolute binary path and a fresh receipt path, and checks the binary
and build manifest against the current source commit. Use a fresh output
location outside the checkout or in a Git-ignored directory for a deliberate
repeat. Build with the declared toolchain; do not silently build the newest
upstream revision or select a different Go release.

[`tools/managed/release.env`](../tools/managed/release.env) owns the managed
version and exact toolchain inputs. The root `go.mod` requires Go 1.26.11, and
CI installs that declared version. The qualification wrapper checks the exact
release toolchain pin; its harness then checks that the manifest records the
same Go version used to compile the harness. The first fork build is
`1.13.1-rr.7`, keeping packaging, license-header and toolchain changes separate
from the frozen `1.13.1-rr.6` artifact. This version assignment alone is not
evidence that a release has been built, qualified or published.

The migration's vulnerability check found
[GO-2026-6859 / CVE-2026-97054](https://pkg.go.dev/vuln/GO-2026-6859) in the
Go 1.26.10 standard library. The new fork build uses the fixed Go 1.26.11
toolchain. This security update applies to `1.13.1-rr.7`; the historical import
commit, Go 1.26.10 reproduction record and approved `1.13.1-rr.6` binary remain
unchanged. Rebuilding the old experiment artifact with a different toolchain
would produce a new experiment input and must not replace the frozen one.

The build must identify the source commit, toolchain, version and build flags
alongside the executable digest. Qualification uses fixed responses from a local
fake provider. Its four cases cover a finding round trip and rejection of a
missing terminal response for both OpenAI Responses and Anthropic Messages.
It establishes managed-process and protocol behavior under those fixtures,
not AI review quality. A passing compilation, unit test or version check alone
does not qualify the real CLI/provider boundary.

For source changes, run the repository's documented checks and the managed
qualification. The native test suite includes the real release-builder test,
so run its final test gate from a clean committed checkout. Record any blocked or unsupported gate accurately. Do not use
provider credentials or start live model evaluation as part of ordinary fork
maintenance. Live evaluation requires its own authorized experiment scope.

The fork's active CI covers native Go and model-free managed qualification.
Upstream workflows are retained under `.github/upstream-workflows/` for review
and later integration. They are not active fork publishing workflows. Upstream
npm packages, plugins, editor extensions and the website have not been qualified
as fork distributions by this extraction.

## Updating upstream

Keep an `upstream` remote pointing to
`https://github.com/alibaba/open-code-review.git`. Fetch its tags without moving
the managed branch. Select an explicit upstream tag and record its full commit
SHA before starting an update branch from the current managed head.

Merge that selected commit into the update branch. Preserve the published
managed history instead of rebasing or force-pushing it. Resolve conflicts by
reviewing the upstream change and the reason for each managed change. Where
upstream now provides the same behavior, remove the redundant fork
implementation only after the relevant managed tests still pass. Avoid applying
the historical patch files again to an already patched tree.

The update PR should identify the old and new upstream revisions, changes to
managed behavior or dependencies, resolved conflicts, build provenance and test
results. Review the delta in workflows, installers and release tooling as well
as Go code. An upstream merge does not authorize publishing upstream npm/editor
packages, enabling live review workflows or changing consumer pins.

Submit managed-fork fixes to this repository. Changes useful to upstream can be
proposed there separately with their original attribution and upstream's
contribution requirements. Keep product-specific policy with its consumer.

Merge a reviewed, passing update before creating a new managed release.
Retain old releases and experiment inputs. Never automatically advance a
consumer from a frozen version to the newest upstream or fork build.

## Releases and consumer handoff

Use explicit managed version tags such as `ocr-v1.13.1-rr.7`. Reserve the
established `ocr-v1.13.1-rr.6` identity for the exact historical artifact. Builds
that change the artifact need a new managed version. Release evidence must
distinguish an imported historical binary from a binary built from this
repository's source commit.

Publish the executable with its checksum and provenance after model-free
qualification succeeds. Publishing a tag, building an artifact and making it
available for anonymous download are separate steps; verify the final download
and digest before directing consumers to it. Do not overwrite an existing
release asset to repair a build.

The `managed-release.yml` workflow runs from `main` and takes the full reviewed
commit as `expected_source_sha`. It reuses `managed-ci.yml` for qualification.
Only its publisher job receives write access, and that job verifies the
qualified artifacts without executing the CLI. The seven release assets are the
binary, its `.sha256` file, `MANIFEST`, `qualification.json`, `LICENSE`, `NOTICE`
and `ReviewRouter-LICENSE`. Their remote draft copies are compared with the
qualified files before publication. If a write fails or its result is unclear,
inspect the existing tag and draft before proceeding; do not blindly rerun a
publication or replace its assets.

For ReviewRouter, update the download location only after the organization
release is accessible and verified. Preserve the admitted version and digest
when only the hosting location changes, and run the consumer's bootstrap and
managed CLI checks. Product runtime activation follows ReviewRouter's own
release and admission process.

For the frozen Task Verification pilot, retain the approved binary digest and
Action parser pin. Hosting migration may change where the same bytes are
downloaded; it must not silently change the evaluated implementation. Future
fork versions can be evaluated as explicitly versioned follow-up experiments.

## Upstream 1.14.0 synchronization (candidate)

Target: `alibaba/open-code-review@39515dca6161fb7fd176d87704b2b10e5f7a0c36`.
This merges upstream history and retains managed ReviewRouter extensions.
Version `1.14.0-rr.1` is a candidate only, not a release or a consumer
migration. The original 1.13.1 import, provenance and approved binary
remain immutable. Native CI, four-case model-free E2E and a code review
must pass before merge or publication.
