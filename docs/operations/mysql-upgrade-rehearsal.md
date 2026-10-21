# MySQL Upgrade and Recovery Rehearsal (D15)

**Run date:** 2026-10-21
**Application source commit:** `507e15beb8b382ea2d0d75e34acde87fa695fc4f`
**Result:** Passed on an isolated MySQL 8.0.46 container using synthetic records.

## Environment and source schemas

- MySQL Server: **8.0.46**, Docker image **mysql@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b**.
- Go toolchain: **go1.26.1 linux/amd64**.
- Historical baseline: tag **v1.2.0**, commit **5b927f8723406b36df206b7b3e7fe6921f72e84e**. Its data_sources model has id, name, type, JSON config, status, and timestamps; it has no owner_id or tenant_id. The name is globally unique through idx_data_sources_name. The fixture DDL and synthetic plaintext password/nested access token are in internal/repository/mysql_upgrade_rehearsal_test.go.
- Recent encrypted baseline: commit **65b32bdda58c165b27ce045d40b4cb4c0ca47ddc**, using the current model.DataSource schema at that revision: ownership columns, the tenant/name unique index idx_datasources_tenant_name, and encrypted credentials in the JSON config. Its fixture includes ciphertext under a retained key and the active key. Key bytes and real connection details are not stored in this report.

The test uses a temporary Docker container and randomly named databases beginning with data_fabric_rehearsal_. It does not connect to a developer or production database.

## Results

| Scenario | Result |
| --- | --- |
| Upgrade v1.2.0-era schema | Added ownership columns; existing ownerless rows retained empty owner/tenant values. The global name index was removed and the tenant/name index was created. |
| Name uniqueness | The same name succeeded in two tenants; a duplicate in one tenant was rejected by MySQL. |
| Ownerless legacy row access | Hidden from a non-admin repository scope and visible to an administrator scope. |
| Plaintext credential migration | Password and nested access token were encrypted at rest and decrypted correctly through the repository. |
| Recent encrypted schema and keyring | A retained-key credential rotated to the active key; active-key ciphertext remained readable. Repeated schema and credential migrations were idempotent. |
| Failed credential migration | The real server process exited before opening its HTTP listener when a required key was missing. A preceding row update was rolled back in MySQL. |
| Backup and restore | Dumped the recent-schema fixture, dropped and recreated only the temporary database, restored it, then verified both rows, decrypted values, and current indexes. |

**Backup checksum from this run:** ef9b79b0e79295c0694b4f96f71386c87a8e3ca27358171b983e74fc1c6dea61. The dump is temporary and removed when the script exits; the checksum is run evidence, not a retained backup artifact.

## Migration behavior found and corrected

The first run exposed an ordering defect in MigrateDataSourceSchema: MySQL exposed the historical single-column unique index while GORM inspected the column, and GORM attempted to drop a nonexistent foreign-key-style constraint. The migration now removes the known legacy index before AutoMigrate, then verifies cleanup afterward. The v1.2.0-to-current MySQL path passed on the rerun.

## Repeat the rehearsal

The script requires Docker, Go, OpenSSL, and Python 3. It prepares the pinned SQLGlot dependency in a temporary directory using the project requirements file and a disposable Python container; it does not install packages into the host Python environment.

From the repository root, run:

    ./scripts/release/rehearse_mysql_upgrades.sh

The script requires Docker, Go, and OpenSSL. It pins the MySQL image by digest by default, reports the exact server version and image digest, and removes its temporary container and files on exit. To use another MySQL image, set MYSQL_IMAGE; record the new version and digest in any report of that run.

## Operational limits

This run covers MySQL 8.0.46 and small synthetic fixtures. It does not establish behavior for MariaDB, other MySQL releases, large production datasets, replicas, or network interruption during DDL. MySQL DDL may commit implicitly; do not assume schema changes roll back as one transaction. Back up and rehearse restoration before upgrading production, preserve every key needed to decrypt stored credentials, and run one application version against the metadata database at a time. Do not let old and new binaries share that database during migration.
