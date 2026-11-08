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

The Dockerfile includes the pinned SQLGlot runtime, project license, third-party notices and dependency pin file. It defaults to non-root UID 10001. The contributor path can still build from source; adopters should use [the digest-pinned deployment overlay](mcp-deployment.md#run-a-published-image-without-compiling-source), which removes the `build` definition and pulls the selected image.

The first tag and package visibility still require maintainer release action. Verify that the GHCR package is public and that its digest, source SHA, release notes and supported profile match before directing adopters to it. The preview profile is linux/amd64 only.

## Current candidate documents

- [v2.0.0 core release candidate review](releases/v2.0.0-candidate.md)
- [Upgrade guide from v1.2.0](upgrading-v1.2.0.md)
