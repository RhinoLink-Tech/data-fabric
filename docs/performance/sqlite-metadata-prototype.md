# Experimental SQLite metadata prototype

[中文](sqlite-metadata-prototype-zh.md) · [Assessment](single-node-metadata-evaluation.md) · [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444)

This source-only developer prototype explores removing the separate metadata MySQL service for a small, single-host installation. It is **not a production deployment, release image or WorkBuddy offering**. MySQL remains the default, existing installations are not migrated, and release mode and replica routing reject SQLite.

Build on a system with a working C compiler and the pinned Go dependencies:

```sh
CGO_ENABLED=1 go build -tags datafabric_sqlite,datafabric_core -o data-fabric ./cmd/server
```

For a development configuration, select `DATABASE_DRIVER=sqlite` and set `DATABASE_SQLITE_PATH` to an absolute file path inside an existing, service-controlled local directory. The final directory must not be writable by group or others; existing database files must be regular files with owner-only permissions. The prototype creates a new file with mode 0600. Use the project's normal authentication and vault configuration; these two settings do not constitute a complete deployable configuration. Never use a network filesystem or an ephemeral container layer. Filesystem locality and security of ancestor directories are the operator's responsibility. Do not expose the prototype publicly.

The SQLite connection enables WAL, FULL synchronous mode, foreign keys and a five-second busy timeout, and starts transactions with IMMEDIATE locking to serialize writers across processes. Tests exercise encrypted synthetic source credentials, policy revisions, audit persistence after restart, and two processes competing to publish a policy. They do not establish correctness for the complete REST/MCP topology, crash recovery, backup and restore, migration from MySQL, all filesystems or all workloads. Never copy only the main SQLite file as a live backup.

The release Dockerfile currently disables CGO; it cannot use this prototype. A build without the opt-in tag and CGO rejects SQLite instead of silently switching storage. Further acceptance, operational design and measurements are tracked in #444 before any release claim.
