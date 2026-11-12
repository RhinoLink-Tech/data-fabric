# Release Artifact Provenance and License Checklist

**Historical review date:** 2026-10-20
**Checked repository commit:** 5b4fcf8383b6310ff42ddb884bc3cb4d1fe44bb3
**Latest release at that historical review:** v1.2.0, tag commit 5b927f8723406b36df206b7b3e7fe6921f72e84e

## Artifact inventory

| Artifact type | Evidence at review | Distribution status | Required materials in the next source release |
| --- | --- | --- | --- |
| GitHub source archive | Releases v1.0.0, v1.1.0, and v1.2.0 have no attached binary assets. GitHub provides source archives from each tag. | Distributed | Root LICENSE (Apache-2.0) and THIRD_PARTY_NOTICES.md (Spec Kit MIT notice). |
| Binary archive | No binary release assets or binary upload step exists in the repository workflow. | Not distributed | No binary package was created for this review. If a binary is selected for a future release, inventory the Go module licenses and package the applicable notices with it. |
| Container image | The Go CI workflow builds data-fabric:ci locally and smoke-tests it; it has no registry login or image push step. The public organization package listing has no data-fabric container entry at review time. | Not publicly distributed by this repository | No image-specific license bundle was added. If container publishing is later selected, include the applicable license and notice files in the published image or its release materials. |

The latest Release page is https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v1.2.0. The organization package listing checked for this review is https://github.com/orgs/RhinoLink-Tech/packages?package_type=container. The GitHub Packages API did not grant this session read:packages access; the public listing and repository workflow were used to check for a public image.

## License and provenance findings

The findings and action list in this section describe the 2026-10-20 review of releases through v1.2.0. The v2.1.0-preview.1 artifact inventory and publication state are recorded in the current-status section below.

- The repository-level LICENSE is Apache License 2.0. README badges also state Apache-2.0.
- The OpenAPI contract incorrectly identified the API license as MIT. The contract is corrected to Apache-2.0 and links to the Apache license text.
- Spec Kit material in the source tree is recorded in THIRD_PARTY_NOTICES.md. At Data Fabric bootstrap commit 51e92036d2cf47cfb73e1ff7926368e01f33a203, five PowerShell scripts and five templates match the GitHub Spec Kit snapshot at 9111699cd27879e3e6301651a03e502ecb6dd65d; nine Claude command files are adaptations of the corresponding upstream command templates. The upstream repository identifies its license as MIT, with copyright GitHub, Inc.
- The reviewed v1.0.0, v1.1.0, and v1.2.0 source archives predate this notice file. Existing tags and generated archives are not rewritten by this change. Any new source release must include the notice file; do not present these old archives as containing it.
- No binary, container image, SBOM, or dependency-license bundle was generated because none of those artifact types is selected for distribution in the current release process.

## Historical next-source-release checklist (2026-10-20)

These were follow-up items after the v1.2.0 review. The later v2.1.0-preview.1 release and its image-specific evidence are documented below.

- [ ] Build the release source archive from the exact reviewed tag/commit.
- [ ] Confirm the archive contains LICENSE and THIRD_PARTY_NOTICES.md.
- [ ] Confirm the OpenAPI contract reports Apache-2.0.
- [ ] Record the tag, commit SHA, and generated archive checksums on the release page.
- [ ] If binary or container distribution is added, complete its artifact-specific dependency/license review before publishing it.

This review does not create an SBOM. SBOM generation remains a separate release-policy task unless a future artifact format or policy requires it.


## Current v2.1.0-preview.1 and container status (updated 2026-11-12)

The artifact inventory and distribution statements above describe the historical 2026-10-20 review; this section is the current status.

- The latest stable GitHub Release is [v2.0.0](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.0.0). The public Preview [v2.1.0-preview.1](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.1.0-preview.1) was published on 2026-11-11. Its annotated tag points to source SHA `570c2975fedbc886a37003a8b64b68caa699678c`; the public linux/amd64 GHCR image digest is `sha256:7d47073fe806879968ed3c66f9d3b2b7b7a2020f419738c2cbb1b89d58f1ebbf`. Anonymous image retrieval and published-image acceptance on a clean GitHub runner passed. Issue #442 remains open for first-adopter evidence; publication is not a stable-release or production-readiness claim.
- PR #463 merged the annotated-semver tag-gated GHCR publisher. It requires successful push CI for the exact master SHA, refuses existing version and source tags, publishes linux/amd64 version and source-SHA references, and attaches BuildKit SBOM and provenance. Ordinary pull requests do not publish.
- The image packages the pinned SQLGlot 30.23.0 runtime, project LICENSE, THIRD_PARTY_NOTICES.md, and dependency pin file. The current #442 candidate also generates an unmodified license-material bundle for every Go module imported by cmd/server and cmd/replica-ingress. MANIFEST.json records module versions, file paths, and SHA-256 values. The build fails if a dependency has no top-level license terms, except sqlflow.org/gohive, whose pinned README declares Apache License 2.0 and is included as license evidence; the full Apache terms ship at /app/LICENSE. This inventory is not a legal compatibility opinion. The material scan also identifies MPL-2.0 license files in github.com/go-sql-driver/mysql, github.com/hashicorp/go-uuid, and github.com/hashicorp/golang-lru/v2; their exact versions and texts are included in the generated manifest/bundle.
- The digest-pinned Compose overlay removes the build definition and pulls the same immutable image for the API and MCP processes. The host needs Docker Compose 2.24+, OpenSSL, and Python 3 standard library for setup/diagnostics; it does not need Go, source compilation, or manual Python package installation.
- Issue #452 is closed with a candidate-matched local synthetic backup/restore and allowed/denied task rehearsal. See the [#452 completion evidence](https://github.com/RhinoLink-Tech/data-fabric/issues/452). This is engineering evidence, not external-adopter evidence.
- PR #480 added the Go dependency license bundle as master commit db6f56d4689229c792c26e6eb370e83b31763bf4; it did not itself publish the later Preview image. The exact-master push run [42567169998](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/42567169998) passed.
- The tag identifies the release artifact's exact source revision. Later changes on master do not alter that immutable image; build and acceptance claims here apply to the published tag and digest above.
- Issue #442 remains open for first-adopter evidence. Earlier local DataSemanticHub synthetic acceptance is recorded in [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442#issuecomment-6785560963); it is distinct from the later clean-runner public-image acceptance and from the independent-operator rehearsal tracked by #446.
