# Managed tracked inventory and context boundary

## Reconciled implementation ownership

The interrupted ReviewRouter work proposed a thirteenth patch against the
frozen 1.13.1 source and reused the `1.13.1-rr.7` identity. That plan predates
the maintained native fork. Its useful regression fixtures and behavior are
ported here as ordinary source changes; that patch is not a second maintained
implementation. ReviewRouter's original twelve-patch builder remains historical
reproduction tooling.

This change starts from the fork's existing `8a70944f567beba9ffc12d1153e33ef5fd60967d`
main commit, after the upstream 1.14.0 merge. It adds no upstream merge and keeps
the current `1.14.0-rr.1` candidate, Go 1.26.11 pin and
`reviewrouter.ocr-outcome/v1` schema. The published `1.13.1-rr.6` and
`1.13.1-rr.7` artifacts and provenance remain immutable.

## Behavior

An exact managed inventory can preserve an already tracked changed path through
the commit/range provider's `.gitignore` filter. It does not synthesize a diff,
admit an ignored untracked file or override the provider directory exclusions.
Paths remain literal and case sensitive. A rename uses its new path, and a
deletion its old path. Workspace mode retains upstream 1.14's existing behavior
of exposing all tracked changes to the later exact selection step.

Preview and review use the same provider construction. Review selection still
excludes secrets, binary files, provider directories and paths outside the
admitted inventory. The immutable diff context contains permitted related and
deleted files, while managed tools cannot read secret or binary diffs. Both
names of an excluded rename stay excluded from file context. For tracked diffs
omitted by `.gitignore`, the provider retains only old/new path and binary
identities so an unassigned secret renamed into an ignored normal path cannot
bypass that boundary. Preview entries, order and counts stay unchanged.

The built-in managed `file_read`, `code_search` and `file_find` tools apply the
same secret-path and provider-directory rules on both comparison sides. File
reads reject binary contents and workspace symlinks to excluded targets. Search
checks each matched file's full contents once before exposing any matching line;
Git's initial binary check alone misses a NUL beyond its initial buffer.
NUL-delimited filenames keep colons or newlines from bypassing path checks.
This is a native tool boundary, not an inventory restriction on related context
or a scanner for secret-looking contents in otherwise permitted files.
Non-managed tools keep their existing behavior.

## Qualification and handoff

Regression tests cover real Git workspace, commit and range selection, ignored
tracked modifications, empty and nonempty deletions, both rename directions,
literal paths, immutable inventory, preview/run agreement, prompt contents and
the actual `file_read_diff` tool loop. Tool tests cover both comparison sides,
permitted deleted/related context, secret rename aliases, unusual filenames,
binary reads and workspace symlinks. A binary fixture places the matching marker
before 8 KB of text and a later NUL to test whole-file validation.

The existing four model-free managed CLI qualification cases retain their
protocol and terminal-response checks. Each positive case now asks the real
CLI to read excluded secret and binary files, search context, request excluded
and permitted diffs, and read an unassigned secret renamed into an ignored normal
filename. The fake provider rejects any request containing the fixture's
excluded-content markers and requires permitted related context to reach a later
request. No model credentials or external provider calls are used.

Run the unchanged native CI and release gates on the reviewed committed source.
Publish only through the existing managed release workflow after a clean
qualification, verify the public artifact and manifest, then separately qualify
the ReviewRouter consumer against that exact digest. Publication alone does not
activate a consumer or advance frozen experiments. Record actual test and
release evidence in the pull requests; this plan does not claim that any gate
has passed.
