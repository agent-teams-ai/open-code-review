# Retained upstream workflows

These files are the unmodified workflow definitions at the imported upstream
revision. GitHub does not execute workflows from this directory.

The managed fork runs `.github/workflows/managed-ci.yml` and publishes its native
Linux/amd64 CLI through `.github/workflows/managed-release.yml`. The upstream
workflows assume Alibaba self-hosted runners, npm/extension publishing identities,
the upstream documentation website, or a live OCR reviewer. Those systems are not
part of this fork's qualified delivery surface.

Native Go source checks, license checks, action pin checks, generated provider
contracts, the pinned vulnerability check, race-enabled tests and the 90 percent coverage gate remain active in
managed CI. Editor extensions, npm installers, ACP and the website retain their
upstream source and history but are not qualified or published by the managed
release. See `docs/managed-fork.md` for the supported scope and upstream merge
procedure. Review every incoming workflow during an upstream merge before adding
anything to the active workflow directory.
