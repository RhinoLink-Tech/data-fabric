# Release Process

## Current automation

The Go workflow checks pull requests and pushes to `master`. It does not publish binaries or container images. GitHub Releases are currently created by a maintainer.

## Release checklist

1. Confirm the scope and resolve blocking security or compatibility issues.
2. From a clean checkout, run:
   ```bash
   go vet ./...
   go test ./...
   go build ./...
   ```
   Also confirm changed Go files are formatted with `gofmt`.
3. Move relevant `[Unreleased]` entries in [CHANGELOG.md](../CHANGELOG.md) under a new `vX.Y.Z` heading and date. Call out breaking changes and migration steps.
4. Check that README, configuration, and supported-source claims match the release code.
5. Create and push an annotated tag, for example `git tag -a v1.3.0 -m "Release v1.3.0"` and `git push origin v1.3.0`.
6. Create a GitHub Release with highlights, fixes, known limitations, and a changelog link.
7. Verify the release page and tag point to the intended `master` commit.

## Container images

Container publishing is not configured, so releases do not currently publish an image automatically. The repository Dockerfile builds with the Go version declared by the project, defaults to port 8080, and runs as a non-root user. Build and smoke-check a local image with the Docker instructions in [README.md](../README.md) before treating a container image as a release artifact.
