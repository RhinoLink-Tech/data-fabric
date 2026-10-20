# Release Process

## Current automation

The Go workflow checks pull requests and pushes to master. It does not publish binaries or container images. GitHub Releases are currently created by a maintainer.

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

Container publishing is not configured, so releases do not currently publish an image automatically. The repository Dockerfile builds with the Go version declared by the project, defaults to port 8080, and runs as a non-root user. Build and smoke-check a local image with the Docker instructions in [README.md](../README.md) before treating a container image as a release artifact.

## Current candidate documents

- [v2.0.0 core release candidate review](releases/v2.0.0-candidate.md)
- [Upgrade guide from v1.2.0](upgrading-v1.2.0.md)
