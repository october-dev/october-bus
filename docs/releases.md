# Releases

October Bus publishes native runtime archives for macOS, Linux, and Windows on amd64 and arm64.

## Tagged runtime releases

A tag matching `v*` runs the full Go and TypeScript validation suite, builds the runtime and conformance runner, creates archives, generates SPDX SBOMs and SHA-256 checksums, attaches GitHub build-provenance attestations, and creates a GitHub release.

The release workflow also requires the tag to identify the merge commit of a main-branch PR with an independent human approval of its final head, submitted before merge by a GitHub owner, member, or collaborator. A trusted reviewer's unresolved changes request blocks release even if a different reviewer approved. Comments and unpublished draft reviews do not replace submitted approvals. Tags on direct commits, unmerged branches, stale approvals, dismissed reviews, post-merge-only approvals, outsider-only approvals, and self-approvals fail verification. This association check is not a replacement for GitHub's code-owner/write-permission rules. The gate supplements live branch and tag protection; it does not configure GitHub settings or create signing identities.

Release binaries embed the version from the tag. Tags containing a hyphen create a prerelease.

Download the archive for your operating system and architecture from the [GitHub releases page](https://github.com/october-dev/october-bus/releases). Verify its SHA-256 value against `checksums.txt`, extract the archive, and place the `october-bus` binary on your `PATH`. Each archive also contains the conformance runner, license, specification, and documentation.

Before creating a tag:

1. confirm the protocol and package versions;
2. run the complete local validation suite;
3. update release notes and migration guidance;
4. verify that no compatibility claim exceeds current public evidence;
5. use an annotated, signed Git tag.

Platform code signing and notarization require the relevant platform identities. Unsigned artifacts must remain clearly identified until those identities and verification steps are configured.

## npm CLI and TypeScript prereleases

The npm package uses pre-1.0 versions. From `0.1.0-next.14`, it contains both the TypeScript SDK and a Node launcher for the native Go daemon. Six exact-version optional dependencies, named `@october-dev/october-bus-{darwin,linux,win32}-{x64,arm64}`, carry the prebuilt executables. The launcher does not download binaries, execute a shell, or fall back to PATH. Linux builds use `CGO_ENABLED=0`, so a separate musl package is unnecessary.

The prerelease workflow accepts reviewed `main` commits, runs Go and SDK validation, cross-builds all six native packages, and packs the SDK/launcher. It then installs the actual tarballs through a temporary registry on Linux, macOS, and Windows. These tests verify automatic optional-package selection, `npm exec`, a real daemon demo, SDK imports, and failure exit codes with install scripts disabled.

Only after those checks pass does the `npm` environment publish the six platform packages, followed by the parent package, using GitHub OIDC and provenance. The parent `next` tag is not updated if a platform publish fails. Reruns accept already-published versions only when their tarball integrity matches exactly. If an artifact differs, bump the parent version instead of trying to overwrite it; platform versions and optional dependency pins are generated from that version. Stable releases still require an approved stable protocol and SDK compatibility policy.

Builds record the source commit, a digest of tracked and non-ignored source files, and the native binary integrity. Packing rejects an outdated version, source, target metadata, or changed binary. The SDK compiles into a new temporary staging directory, excluding stale output and published lifecycle/dev scripts. Every `.tgz` has a `.tgz.json` record identifying the checked package, required executable path, source, and SHA-512 integrity. These records travel with the CI artifacts; publishing rejects missing, changed, or mismatched records before contacting npm. They detect accidental artifact mixups, not a malicious replacement of both the workflow and its records; GitHub review, protected artifacts and provenance remain the trust boundary.

The publisher then preflights **all seven** immutable versions before its first registry write. An existing-content mismatch or a registry/authentication error at the final package cannot partially publish the earlier packages. Publishing itself is not transactional: a failure during the write phase can leave some native versions published. Preserve the exact tarballs and records for a retry. Identical existing versions are not retagged, avoiding accidental rollback of another release's `next` tag.

### First-publication setup

The six new package names require maintainer ownership and trusted-publisher configuration for this repository's `publish-npm-prerelease.yml` workflow and `npm` environment, just like the parent package. Complete npm's initial package publication/ownership setup and configure each trusted publisher before attempting the first seven-package release. Authentication failures must not be worked around by publishing a parent package whose binaries are unavailable or disabling provenance. See [npm trusted publishing](https://docs.npmjs.com/trusted-publishers/).

### Local package validation

From `sdk/typescript`:

```sh
npm ci --ignore-scripts
npm run test:errors
npm run build:native
npm run pack:distribution
npm run test:distribution
```

Pass `-- --all` to the build and pack commands for every platform. Generated manifests, binaries, tarballs, and artifact records live under ignored `dist/npm/`; do not commit them. Finish source edits and commit before building release artifacts: a new commit or source change invalidates old build stamps. Packing stages the parent manifest with its six exact-version optional dependencies. They are deliberately absent from the source lockfile so `npm ci` works before a new version's platform packages exist; publishing directly from the source SDK directory is blocked. The bundled binary embeds the npm version and is built from the same commit as its SDK, including the session-retirement endpoint needed by the updated SDK. Native Go release tags and archives retain their own versioning.

The combined validation and actual-binary upgrade rehearsal are documented in [launch validation](launch-validation.md). Neither command publishes or downloads harnesses. Publication, public-registry installation tests, independent harness evidence and signing still need separate sign-off. Never move `latest` merely to make an unqualified `npx` command work; keep prerelease documentation explicit about the version or `next` tag until a stable release is approved.
