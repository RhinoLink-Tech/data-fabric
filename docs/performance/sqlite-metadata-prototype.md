# Experimental SQLite metadata prototype

[中文](sqlite-metadata-prototype-zh.md) · [Assessment](single-node-metadata-evaluation.md) · [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444)

This source-only developer prototype explores removing the separate metadata MySQL service for a small, single-host installation. It is **not a production deployment, release image or WorkBuddy offering**. MySQL remains the default, existing installations are not migrated, and release mode and replica routing reject SQLite.

Build on a system with a working C compiler and the pinned Go dependencies:

```sh
CGO_ENABLED=1 go build -tags datafabric_sqlite,datafabric_core -o data-fabric ./cmd/server
```

For a development configuration, select `DATABASE_DRIVER=sqlite` and set `DATABASE_SQLITE_PATH` to an absolute file path inside an existing, service-controlled local directory. The final directory must not be writable by group or others; existing database files must be regular files with owner-only permissions. The prototype creates a new file with mode 0600. Use the project's normal authentication and vault configuration; these two settings do not constitute a complete deployable configuration. Never use a network filesystem or an ephemeral container layer. Filesystem locality and security of ancestor directories are the operator's responsibility. Do not expose the prototype publicly.

The SQLite connection enables WAL, FULL synchronous mode, foreign keys and a five-second busy timeout, and starts transactions with IMMEDIATE locking to serialize writers across processes. Tests exercise encrypted synthetic source credentials, policy revisions, audit persistence after restart, and two processes competing to publish a policy. They do not establish correctness for the complete REST/MCP topology, every crash/recovery case, migration from MySQL, all filesystems or all workloads. A process-kill test verifies rollback of one interrupted write, and an online snapshot test covers committed WAL data, encrypted source credentials, policy revocation and audit records. These are limited synthetic tests, not a complete disaster-recovery guarantee.

## Experimental backup and restore

Use the same CGO-enabled experimental binary. The destination's parent must already exist, be service-controlled and not be group/other writable. The snapshot and restore target must be new paths without existing journal sidecars; the command never overwrites an existing file. The backup uses SQLite `VACUUM INTO` to capture a consistent snapshot while the source is running, rather than copying the live database file.

```sh
./data-fabric sqlite-metadata backup --source /srv/data-fabric/metadata.sqlite --output /srv/data-fabric/backups/metadata-20261120.sqlite
# Stop all processes that could access the restore target before restoring.
./data-fabric sqlite-metadata restore --source /srv/data-fabric/backups/metadata-20261120.sqlite --output /srv/data-fabric/restored.sqlite
```

If the command reports a failure after publishing the file, inspect and remove that new file before retrying; it never overwrites it. The output is owner-only but still contains sensitive metadata. Protect it and store credential vault keys separately using your normal key-backup procedures: keys are not included in the snapshot, and the restored deployment must have the correct keyring. Validate the restore in an isolated environment and configure the service to use the new path only after verification. These commands do not replace an existing database or implement MySQL migration. Never copy only the main SQLite file as a live backup.

The release Dockerfile currently disables CGO; it cannot use this prototype. A build without the opt-in tag and CGO rejects SQLite instead of silently switching storage. Further acceptance, operational design and measurements are tracked in #444 before any release claim.
