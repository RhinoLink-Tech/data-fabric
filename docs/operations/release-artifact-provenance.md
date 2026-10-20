# Release Artifact Provenance and License Checklist

**Review date:** 2026-10-20
**Checked repository commit:** 5b4fcf8383b6310ff42ddb884bc3cb4d1fe44bb3
**Latest published Data Fabric release reviewed:** v1.2.0, tag commit 5b927f8723406b36df206b7b3e7fe6921f72e84e

## Artifact inventory

| Artifact type | Evidence at review | Distribution status | Required materials in the next source release |
| --- | --- | --- | --- |
| GitHub source archive | Releases v1.0.0, v1.1.0, and v1.2.0 have no attached binary assets. GitHub provides source archives from each tag. | Distributed | Root LICENSE (Apache-2.0) and THIRD_PARTY_NOTICES.md (Spec Kit MIT notice). |
| Binary archive | No binary release assets or binary upload step exists in the repository workflow. | Not distributed | No binary package was created for this review. If a binary is selected for a future release, inventory the Go module licenses and package the applicable notices with it. |
| Container image | The Go CI workflow builds data-fabric:ci locally and smoke-tests it; it has no registry login or image push step. The public organization package listing has no data-fabric container entry at review time. | Not publicly distributed by this repository | No image-specific license bundle was added. If container publishing is later selected, include the applicable license and notice files in the published image or its release materials. |

The latest Release page is https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v1.2.0. The organization package listing checked for this review is https://github.com/orgs/RhinoLink-Tech/packages?package_type=container. The GitHub Packages API did not grant this session read:packages access; the public listing and repository workflow were used to check for a public image.

## License and provenance findings

- The repository-level LICENSE is Apache License 2.0. README badges also state Apache-2.0.
- The OpenAPI contract incorrectly identified the API license as MIT. The contract is corrected to Apache-2.0 and links to the Apache license text.
- Spec Kit material in the source tree is recorded in THIRD_PARTY_NOTICES.md. At Data Fabric bootstrap commit 51e92036d2cf47cfb73e1ff7926368e01f33a203, five PowerShell scripts and five templates match the GitHub Spec Kit snapshot at 9111699cd27879e3e6301651a03e502ecb6dd65d; nine Claude command files are adaptations of the corresponding upstream command templates. The upstream repository identifies its license as MIT, with copyright GitHub, Inc.
- The reviewed v1.0.0, v1.1.0, and v1.2.0 source archives predate this notice file. Existing tags and generated archives are not rewritten by this change. Any new source release must include the notice file; do not present these old archives as containing it.
- No binary, container image, SBOM, or dependency-license bundle was generated because none of those artifact types is selected for distribution in the current release process.

## Next source-release checklist

- [ ] Build the release source archive from the exact reviewed tag/commit.
- [ ] Confirm the archive contains LICENSE and THIRD_PARTY_NOTICES.md.
- [ ] Confirm the OpenAPI contract reports Apache-2.0.
- [ ] Record the tag, commit SHA, and generated archive checksums on the release page.
- [ ] If binary or container distribution is added, complete its artifact-specific dependency/license review before publishing it.

This review does not create an SBOM. SBOM generation remains a separate release-policy task unless a future artifact format or policy requires it.
