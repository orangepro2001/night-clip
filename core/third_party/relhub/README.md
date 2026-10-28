# relhub (vendored SDK of release-publish)

`pkg/relclient` (client: check, watch, download with resume and verification, publish) and
`pkg/relproto` (wire types, the signed manifest, Ed25519 verification) of the local
**release-publish** service, copied verbatim. Standard library only.

- **Do not edit here.** Change the SDK in `~/dev/release-publish`, then run
  `scripts/sync-relhub.sh` from the repository root. `REVISION` records the commit that was copied.
- Only the sources are copied: the SDK's own tests start the server from its `internal/` packages and
  cannot build outside that repository. `core/update` is tested against a fake server
  (`core/update/updatetest`) and, when a `relhub` binary is installed, against the real one
  (`TestAgainstRealRelhub`).
- Why a copy and not `replace relhub => /home/tony/dev/release-publish`: the repository is public and
  built by GitHub CI, which has no such path. The same pattern is used for `third_party/gokeepasslib`.
- Used only by `core/update` (`go.mod`: `require relhub v0.0.0` + `replace relhub => ./third_party/relhub`).

How the app uses the service, and what had to be added around the SDK: `docs/update.md`,
`docs/release-publish-integration.md`.
