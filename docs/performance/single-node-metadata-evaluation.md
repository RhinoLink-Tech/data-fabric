# Single-node metadata: SQLite candidate assessment

[中文](single-node-metadata-evaluation-zh.md) · Tracking: [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444)

Status: **an experimental source-only prototype is available**; it is not a supported release backend. See the [prototype notes](sqlite-metadata-prototype.md). The maintainer wants simple installation and cannot download large images locally. The current reference deployment starts a separate MySQL metadata service for the REST and MCP processes. Its source databases and optional Trino federation engine are separate again; the gateway-only size comparison in [#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448) does not measure their resources.

On the maintainer's Docker host, the already-cached `mysql:8.4.13` image reported `Size=1,102,620,445` bytes and image ID `sha256:a81d043930e51745522bd5c430961cf8286f0a23f0a9a2a6e0108f7ea71b0902` on 2026-11-19. This is one local Docker-reported value, not a registry transfer size, memory measurement, production estimate or cross-host comparison. No image was pulled for this observation. It establishes a concrete installation concern, not a measured adoption conversion or expected SQLite saving.

## Existing constraints

- [`InitDatabase`](../../internal/config/database.go) opens the metadata store through the MySQL GORM driver. The configuration has host/port/user/password fields and no selectable production SQLite backend. SQLite use in existing tests or individual store code is not a shipped deployment.
- The release [Dockerfile](../../Dockerfile) builds with `CGO_ENABLED=0`. With the currently pinned `gorm.io/driver/sqlite` / `mattn/go-sqlite3`, an offline Go 1.26.1 synthetic `:memory:` ping under `CGO_ENABLED=0` returned the driver's nonfunctional stub error. Merely choosing that existing test dependency cannot create a working release binary.
- Source update, browser authorization, onboarding grants and policy publication use database transactions and GORM update locks; see [source update](../../internal/repository/datasource_locked_update.go), [browser state](../../internal/security/browserauth/store.go), [onboarding grants](../../internal/security/onboarding/grant_store.go) and [policy publication](../../internal/security/table_policy_manager.go). The currently pinned SQLite GORM dialector deliberately omits row-level `FOR UPDATE`. Test-specific SQLite support therefore does not establish the same cross-process authorization, revocation or publication guarantees. SQLite's actual locking and failure behavior must be measured and designed explicitly; a one-connection pool per process alone is not a cross-process proof.

## Options and recommendation

| Option | User benefit | Work before shipping |
| --- | --- | --- |
| Keep current MySQL metadata | Existing supported reference and shared policy behavior; no migration. | Separate service/image and credentials; existing cached image can be reused. |
| Optional SQLite, one host | Could remove the metadata server/image for a small self-hosted installation while retaining a durable file. | Choose and verify either a CGO-enabled SQLite build or a reviewed pure-Go driver/dialector. Confirm licensing, cross-builds, image and runtime cost. Design transaction serialization and busy/error handling for the actual REST/MCP topology. |
| Combined REST/MCP process [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443) | Might reduce processes and setup steps. | Independent evaluation; it does not itself remove MySQL and cannot replace the SQLite correctness work. |

Continue the **single-host opt-in prototype** and review its transaction contract and driver/build choice before release. Keep MySQL as default and preserve existing data. An embedded file is not a distributed database, multi-host shared store, HA service or transparent upgrade from MySQL. Mount it on one supported local durable filesystem; do not silently use container ephemeral storage or a network filesystem.

## Small, useful proof

1. Pick a driver and pin it. Prove a release-equivalent Linux build can open the file and record binary/image sizes for the selected build. Keep credentials encrypted by the existing vault; retain the same secret-key requirements. Do not assume an existing SQLite test establishes release compatibility.
2. Define connection, file ownership, migrations, journal mode, durability, timeout and shutdown behavior. Give clear, secret-free errors for read-only directories, full disk, corrupt/unavailable database, missing keys and lock contention. Design an explicit online-consistent or offline-stopped backup and restore; never assume copying just the database file while it is active is safe.
3. Exercise the actual intended process topology on one host: create a source with synthetic credentials, restart, list it from REST and MCP, query with source/tenant and table/column policies, revoke access, record audit and restore from a backup. Check source IDs, encrypted credentials, policy revisions and audit records survive. Do not use Atlas data.
4. Run meaningful two-process mutation/revocation and competing policy-publication tests, including forced termination and interrupted transactions. Establish a serial order or fail closed with an actionable error; preserve CAS and audit behavior. These are correctness checks for normal simultaneous operators, not a high-throughput benchmark.
5. Compare the same user journey and whole-stack image sizes, startup, idle memory, persistent storage and support/backup burden against the current MySQL profile. Record exact images, source revision, configuration and host; do not confuse cache size with registry transfer or claim benefits before measurement.

Actual release and user-facing WorkBuddy claims require the full #444 contracts and the real client journey. The proposal does not modify the existing metadata backend, publish an image or migrate user data.
