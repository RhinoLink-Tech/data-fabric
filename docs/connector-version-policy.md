# Connector version and support policy

[中文](connector-version-policy-zh.md) · [Current support catalog](data-source-support.md) · [Contract evidence](driver-contract-tests.md)

Use this policy to decide whether a specific source, version and operation fit a deployment. It describes evidence and project maintenance, not a production SLA or a promise to support a product for its vendor's entire lifecycle.

## Read a support entry

1. Identify the Data Fabric release or image digest actually deployed. The source catalog generated from another commit can differ from that artifact.
2. Look up the exact connector type and version in `/api/v1/database/types`, or call MCP `list_source_types` with `type` and `include_details: true`. Read version policy, operations, authentication disclosure and limitations together.
3. Compare the detected server product and exact version with `verifiedServerVersions` (MCP `verified_server_versions`). Detection is best effort; an absent version is unknown, not a match.
4. Check the exact authentication, TLS, query and topology evidence before depending on an operation. Connection success alone is not a contract test.

| Meaning | How to recognize it | What it means |
| --- | --- | --- |
| Exact version verified | `endToEndVerified` / `end_to_end_verified` is true and the actual product/version is in the verified list | The recorded contract passed for that artifact and scenario. It does not cover all configurations or operations. |
| Declared supported range | A nonempty `supportedVersionRange` / `supported_version_range` and its `versionPolicy` explicitly state a range | Apply only to the stated product, deployment and operations. `only` followed by a list is an exact-version list, not a continuous range. |
| Not yet verified | Version is absent from the evidence or detection is unavailable | Compatibility is unknown. The service may allow an attempt; neither success nor failure can be inferred from the version alone. |
| Explicitly unsupported | `status` is `unsupported`, or a documented limitation rejects the requested operation/configuration | Do not offer that operation as available. Follow the stated limitation or choose a supported path. |

`status` is a connector-level lifecycle classification, not a per-version verdict. `experimental` can coexist with exact-version evidence for a limited adapter. The legacy REST `supported` field means the driver is registered; it is not a compatibility promise. `registered` and `implementation_present` do not prove a working connection.

## Pilot: PostgreSQL and MySQL

The authoritative exact lists and scope are the [generated catalog](data-source-support.md) and the [pinned contract report](driver-contract-tests.md#verified-baselines). For example, the current catalog records PostgreSQL 17.11 and MySQL 8.4.13 among tested versions. PostgreSQL 17.11 also has a `verify-full` TLS contract; the MySQL 8.4.13 contract covers trusted CA and hostname verification. These are example evidence points, not claims that every 17.x or 8.4.x release is supported.

Candidate ranges for a future policy decision are PostgreSQL 15–18 and MySQL 8.0/8.4, because the existing matrix contains individual versions in those families. They are **not declared supported ranges**. Before any range is published, maintainers must define its endpoints, authentication and operation scope, run an agreed reproducible version matrix against those boundaries, investigate failures and document the residual limits. Testing a few patches alone does not establish every patch in a range. New tests can run on CI or approved existing infrastructure; no local image pull is required by this policy.

## Maintaining and retiring evidence

- The connector maintainer proposes exact version or range changes with source code revision, Data Fabric artifact, source product/version and immutable runtime artifact, supported operation and authentication/TLS/topology scope, and a link to reproducible pass/fail results. A skipped test is not a pass. Never include credentials or real data.
- A maintainer reviews the evidence and updates the runtime catalog, generated support page, contract report and release notes together. A source release's status changes only when its own evidence supports the change.
- Source vendors' maintenance or end-of-support announcements should trigger a maintainer review; they do not automatically change Data Fabric status or establish a Data Fabric support deadline. Record any decision, date, migration guidance and replacement evidence in the release notes and catalog. Until a project-specific decision is published, no end-of-support date or support window is promised.
- On a regression or withdrawn evidence, make the affected range/operation explicit, retain historical results and provide an actionable workaround. Do not broaden support to hide a failing version.

To add an evidence point, start with [driver contract tests](driver-contract-tests.md), use an isolated synthetic fixture and record skipped and failed outcomes as well as passes. Project-wide release and recovery claims remain separate from individual connector compatibility.
