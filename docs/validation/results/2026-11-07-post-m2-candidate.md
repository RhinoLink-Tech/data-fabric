# Post-M2 Candidate Validation Record (2026-11-07)

This report closes the operational evidence loop for issue #416. It covers a synthetic reference deployment and recovery rehearsal; it does not claim a real-team pilot, production release, or published container artifact.

## Candidate and environment

| Field | Observed value |
| --- | --- |
| Candidate source | **53d0d662ac24273d8a5877dd4190188c843342c8** (master; PR #420); checkout was clean before build and validation |
| Local gateway image | **data-fabric:candidate-53d0d662ac24**, image ID **sha256:c7a098cb4b879c4896b07a4fb4e3184888f657038620752927679004b36a2585** |
| Runtime checkout reported by acceptance | **53d0d662ac24273d8a5877dd4190188c843342c8** |
| Environment | Linux/amd64, Docker 29.8.0, Compose 5.5.1, Go 1.26.1, MCP client github.com/mark3labs/mcp-go v0.43.2 |
| Reference MySQL (metadata and source) | MySQL 8.4.13, mysql:8.4.13@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7 |
| Reference PostgreSQL | PostgreSQL 17.11, postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24 |

The reference profile used REST and MCP owner replicas set to 2, a 3-second owner drain timeout, an 8-second query-session TTL, a 10-second routed MCP-session TTL, a 32-session routed MCP limit, and tenant query capacity 2. Fixture data, identities, database credentials, and credential key material were synthetic and runtime-only.

BuildKit emitted a non-fatal source-control metadata warning because the local Git object graph lacked secondary PR-head commit cb7f2ab7. The acceptance runtime reported the exact candidate SHA above, and the local image ID is recorded independently. This image was not pushed to a registry; build from a complete clone when reproducing the run.

## Deployment and second-application reuse

The reference harness registered the PostgreSQL and MySQL sources once under app-a, then used an independent same-tenant app-b identity with the same governed policy. Cross-tenant access, a restricted column, and an invalid MCP audience were rejected.

- Clean setup and smoke: 53.677 seconds.
- Source registration under app-a: 43 ms.
- app-b reused both registered sources through REST and MCP: 564 ms.

These are one local synthetic run's timings, not service-level objectives.

## Candidate acceptance

The full command completed successfully against the image above:

    go run ./deploy/mcp/reference/cmd/acceptance -image data-fabric:candidate-53d0d662ac24 -multi-replica

- 66 records passed, 5 were explicitly unsupported, and 0 failed.
- The five unsupported records were REST A03 and A04 (no policy-filtered public table listing or description) and REST A14-A16 (the REST API uses HS256 rather than the OIDC authentication exercised by those cases). Their MCP counterparts passed. These remain explicit REST limitations.
- All recorded two-replica routing, continuation, drain/retirement, cumulative-budget, policy revocation, rollback, and policy-history checks passed.

The exact master commit's [Go CI run 42321263987](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/42321263987) completed successfully. It includes successful unit/build checks, public REST/MCP reference acceptance, PostgreSQL/MySQL reference contracts, and affected-contract selection. The candidate was merged as [PR #420](https://github.com/RhinoLink-Tech/data-fabric/pull/420).

## MySQL upgrade and recovery

The MySQL recovery run executed scripts/release/rehearse_mysql_upgrades.sh and passed on source SHA **53d0d662ac24273d8a5877dd4190188c843342c8** using Go 1.26.1 and MySQL 8.0.46, image mysql@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b.

The rehearsal passed the v1.2.0 plaintext and recent encrypted-schema upgrade paths, key rotation, missing-key fail-closed startup and transaction rollback, managed-policy grant/revocation/rollback, and backup/restore. After restore, source metadata and indexes were present, credentials remained decryptable with the synthetic retained keyring, active policy revision 3 and historical revoked revision 2 were present, and publication history retained the revocation and rollback publisher/outcome records. The recovery fixture used synthetic key ID current; no secret key value is included here.

- Backup SHA-256: **0422f5db24690676b613c6219ac8079984dcd87e86fe634efe46c1b6e0582835**.
- Result: MySQL upgrade and recovery rehearsal passed.

## Support boundary

The support catalog already lists MySQL 8.0.46 and 8.4.13 and PostgreSQL 17.11 among its exact end-to-end-verified versions. This candidate run adds no version range or connector claim. Results apply to the pinned synthetic reference profile and the recorded versions; they do not establish production readiness, live-customer fit, or support for other versions.

This record is linked from the M3 roadmap and gateway delivery review. The procedure for repeating the run is in the [candidate evidence runbook](../post-m2-candidate-evidence.md).
