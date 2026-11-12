# Release Process

## Current automation

The Go workflow checks pull requests and pushes to master. The `publish-container.yml` workflow publishes only on an annotated semver tag that points to master; pull requests do not publish images. GitHub Releases are still created by a maintainer.

## Release checklist

1. Confirm the frozen scope, version, and compatibility changes. Resolve all P0 security issues and release-blocking compatibility issues. Record a written risk decision for each P1 issue judged non-blocking.
2. Check that every advertised runtime capability is available in the shipped artifact. In particular, do not advertise Python-backed SQL translation in a container that does not package its runtime.
3. From a clean checkout of the exact candidate SHA, run:
   ```bash
   go vet ./...
   go test ./...
   go build ./...
   ```
   Also confirm changed Go files are formatted with gofmt. Link the successful CI run and any applicable integration/recovery rehearsal.
4. Confirm README, configuration, and the registered-source support matrix match the release code. Include the versioned upgrade and recovery guide.
5. Move relevant Unreleased entries in [CHANGELOG.md](../CHANGELOG.md) under the selected version and date. Call out breaking changes and migration steps.
6. Review the source, binary, and container artifact inventory and attach applicable license materials. Follow the [release artifact provenance checklist](operations/release-artifact-provenance.md).
7. From the exact reviewed SHA, prepare the selected artifacts and record SHA-256 checksums. The source archive must contain LICENSE and THIRD_PARTY_NOTICES.md.
8. Create and push an annotated tag, for example git tag -a vX.Y.Z -m "Release vX.Y.Z" and git push origin vX.Y.Z.
9. Create a GitHub Release with highlights, fixes, known limitations, the upgrade guide, changelog link, source commit, and checksums.
10. Verify the release page and tag point to the intended master commit and the downloadable source archive contains the required license materials.

## Container images

The tag-gated `publish-container.yml` workflow builds one `linux/amd64` GHCR image from an annotated semver tag on master. Before building, it requires a successful Go CI push run for the exact SHA with successful affected-contract selection, unit/build, PostgreSQL/MySQL reference contracts, and public REST/MCP acceptance. It rejects an existing version or source tag, publishes version and source-SHA tags, and attaches BuildKit SBOM and max-mode provenance. The workflow summary records the source SHA and immutable `tag@sha256` reference. Pull requests do not publish images.

The Dockerfile includes the pinned SQLGlot runtime, project license, third-party notices, dependency pin file, and a generated Go-module license bundle for both shipped binaries. Its manifest binds the unmodified license materials to module versions and SHA-256 values; the image build fails when a dependency has no license terms, with the documented GoHive README exception. It defaults to non-root UID 10001. The contributor path can still build from source; adopters should use [the digest-pinned deployment overlay](mcp-deployment.md#run-a-published-image-without-compiling-source), which removes the build definition and pulls the selected image.

### v2.1.0-preview.1 publication record

The maintainer approved v2.1.0-preview.1 and its linux/amd64, single-host MySQL profile on 2026-11-09. The Preview was published on 2026-11-11 as [GitHub Release v2.1.0-preview.1](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.1.0-preview.1). Its annotated tag points to source SHA `570c2975fedbc886a37003a8b64b68caa699678c`; the public image digest is `sha256:7d47073fe806879968ed3c66f9d3b2b7b7a2020f419738c2cbb1b89d58f1ebbf`. The published-image acceptance workflow passed the documented install with Go removed from PATH, exact image revision/platform checks, REST/MCP acceptance, and two-replica acceptance. Issue #442 remains open for first-adopter evidence, and #446 tracks the separate independent-operator rehearsal.

For a future container release, verify public visibility, source SHA, digest, SBOM, provenance, and the supported profile after the tag workflow publishes the image. Then dispatch the [published-image acceptance workflow](../.github/workflows/release-image-acceptance.yml) from that release tag with its exact tag@sha256 reference. Create the GitHub Release after that acceptance passes. The current Preview profile is linux/amd64 only.

## Current candidate documents

- [Maintainer-approved v2.1.0-preview.1 Preview candidate review](releases/v2.1.0-preview.1-candidate.md)
- [中文候选评审](releases/v2.1.0-preview.1-candidate-zh.md)
- [v2.0.0 core release candidate review (historical snapshot)](releases/v2.0.0-candidate.md)
- [Upgrade guide from v1.2.0](upgrading-v1.2.0.md)
