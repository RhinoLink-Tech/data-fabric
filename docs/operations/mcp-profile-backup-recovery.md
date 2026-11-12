# Self-Hosted MCP Profile Backup and Recovery

This runbook covers the current single-host profile: MySQL metadata, a pinned Data Fabric image, the credential-vault keyring and the OIDC configuration. Use it with the immutable candidate recorded in the backup manifest. It does not back up business-source databases, query results, live cursors, logs or the TLS private key.

The backup contains the deployment `.env` and the complete `data_fabric` metadata database. Treat the encrypted archive as a secret. The metadata rows contain encrypted source credentials; they are useful only with every key in the recorded keyring. A syntactically valid keyring does not prove that every stored credential can be decrypted, so the restore rehearsal includes a selected-source connection check.

## Prerequisites and key custody

- Python 3.10+, Docker Engine with Docker Compose 2.24+, `age`, and a clean Git checkout of the exact candidate source revision; a source archive alone has no verifiable commit identity.
- An `age` public recipient for the backup operator. Generate and keep the private identity on a separate recovery device or in a secret manager; do not place it on the application host beside the archive.
- An owner-only staging directory on encrypted storage or a memory-backed filesystem. The CLI writes plaintext SQL and `.env` into this directory temporarily and removes it when finished. File deletion is not guaranteed secure erasure on SSDs or copy-on-write filesystems. A forced termination or host failure may leave plaintext in staging; treat leftovers as sensitive, do not share them, and clean them according to local policy.
- An encrypted backup destination outside `deploy/mcp/`. Copy the encrypted archive off-host after creation and retain its checksum separately.

Create the private identity on the recovery device, then provide only its public recipient to the application host:

```bash
umask 077
install -d -m 700 /secure/recovery
age-keygen -o /secure/recovery/data-fabric.agekey
age-keygen -y /secure/recovery/data-fabric.agekey > /secure/recovery/data-fabric.recipients
chmod 600 /secure/recovery/data-fabric.agekey /secure/recovery/data-fabric.recipients
```

The recipient is public. The identity file is private and must remain separate from the archive.

## Create an encrypted backup

Run `onboard.py doctor` first and resolve all reported failures. Then run from a clean candidate checkout. The helper requires `deploy/mcp/.env` to be a regular mode-0600 file and validates the active 32-byte key plus every retained key. It records the source SHA, the image ID/reference actually used by API and MCP, metadata MySQL image identity/version, key IDs, and payload checksums in an encrypted manifest. It also requires the image's OCI revision label to match the clean Git checkout SHA.

```bash
install -d -m 700 /mnt/encrypted-staging/data-fabric /mnt/encrypted-backups/data-fabric
python3 scripts/mcp/recovery.py backup \
  --recipient "$(cat /secure/recovery/data-fabric.recipients)" \
  --staging-dir /mnt/encrypted-staging/data-fabric \
  --output "/mnt/encrypted-backups/data-fabric/profile-$(date -u +%Y%m%dT%H%M%SZ).age"
```

The helper stops only the selected Compose project's running API and MCP services while it snapshots metadata, then restarts the services that were running before the backup. The metadata database stays online and uses a single-transaction dump. Do not rotate keys, migrate schemas, or run a second writer against the metadata database during the snapshot. If the machine stops abruptly, check and restart the two application services manually.

The command prints the encrypted archive checksum, candidate and database identities, and key IDs. It never prints the `.env`, key bytes, SQL dump, database password, or source credentials. Store the checksum with the archive through a separate channel. Keep the private age identity elsewhere.

## Verify before transfer or restore

On a recovery host with access to the private identity, decrypt into a protected staging directory and validate the archive members, manifest, checksums and keyring inventory:

```bash
python3 scripts/mcp/recovery.py verify \
  --archive /mnt/encrypted-backups/data-fabric/profile-20261108T000000Z.age \
  --identity-file /secure/recovery/data-fabric.agekey \
  --staging-dir /mnt/encrypted-staging/data-fabric
```

The verifier emits only non-secret provenance and key IDs and removes its temporary plaintext files afterward. Verify the archive SHA-256 against the value recorded when it was copied. A successful archive check does not prove source credentials decrypt or that the external databases are reachable; those are restore acceptance checks.

## Prepare a fresh restore

Use a fresh checkout at the exact `source_revision` shown by `verify`. Do not run this command in the existing deployment directory. The target `deploy/mcp/.env` must not exist, its `tls/` directory must be absent or empty, and its `recovery/` directory must be unused. The command refuses to overwrite any of them. It writes the recovered `.env` and SQL dump with owner-only permissions, changes only host ports and the TLS group ID for the isolated restore project, and does not start containers or import into a database.

```bash
git clone https://github.com/RhinoLink-Tech/data-fabric.git /srv/data-fabric-restore
cd /srv/data-fabric-restore
git checkout <exact-source_revision-from-verified-manifest>
python3 scripts/mcp/recovery.py restore \
  --archive /mnt/encrypted-backups/data-fabric/profile-20261108T000000Z.age \
  --identity-file /secure/recovery/data-fabric.agekey \
  --staging-dir /mnt/encrypted-staging/data-fabric \
  --target-deploy-dir deploy/mcp \
  --project-name data-fabric-restore-20261108 \
  --api-port 18080 --mcp-port 18443
```

The archive's TLS certificate/private key is intentionally not restored. `onboard.py init` generates a new localhost pair for this isolated instance. For a real hostname, install a matching certificate and update the TLS group before exposing the endpoint. Existing production profile and volumes remain untouched. Before restore preparation, the target host must already have the exact app image ID in the manifest: pull the recorded tag@sha256 for a registry candidate, or build/import the same-SHA local image and confirm its ID. The backup does not contain image layers.

From the repository root, initialize TLS, import the SQL dump into the new project's empty MySQL volume, then start the complete profile. Keep the project name unique and both ports bound to loopback for recovery checks:

```bash
cd deploy/mcp
python3 ../../scripts/mcp/onboard.py init --directory "$PWD"
# Repeat initialization; existing keys, configuration and TLS files must be preserved.
python3 ../../scripts/mcp/onboard.py init --directory "$PWD"
export COMPOSE_PROJECT_NAME=data-fabric-restore-20261108
compose_files=(-f docker-compose.yml -f docker-compose.recovery.yml)
if grep -Eq '^DATA_FABRIC_IMAGE=.*@sha256:[0-9a-f]{64}$' .env; then
  compose_files=(-f docker-compose.yml -f docker-compose.release.yml -f docker-compose.recovery.yml)
fi
compose=(docker compose --project-name "$COMPOSE_PROJECT_NAME" --env-file .env "${compose_files[@]}")
"${compose[@]}" up --no-build -d metadata-db
db_container="$("${compose[@]}" ps -q metadata-db)"
docker inspect --format '{{.Image}}|{{.Config.Image}}' "$db_container"  # compare with the MySQL image ID/reference in the manifest
"${compose[@]}" exec -T metadata-db sh -ec \
  'until mysqladmin ping -h 127.0.0.1 -uroot -p"$MYSQL_ROOT_PASSWORD" --silent; do sleep 2; done'
"${compose[@]}" exec -T metadata-db sh -ec \
  'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --user=root' < recovery/metadata.sql
"${compose[@]}" up --no-build -d
python3 ../../scripts/mcp/onboard.py doctor --directory "$PWD"
```

If `DATA_FABRIC_IMAGE` in `.env` is a tag@sha256 reference, use the release overlay so Compose pulls that exact image. If the backup records the local `data-fabric:local` candidate, omit the release overlay and confirm the local image ID matches the manifest. In both cases include `docker-compose.recovery.yml`; it binds the MCP port to loopback. Confirm Docker's actual app and MySQL image IDs and app revision label match the manifest. Do not reuse an existing Compose project name or database volume. Do not point an older binary at the restored/migrated database; rollback means leaving this project isolated and restoring into another fresh project from a verified backup.

## Verify allowed and denied work

Use two separately issued OIDC access tokens whose claims follow the configured tenant mapping. Use the same source identity and read-only query in both requests:

1. An allowed principal in the source owner's tenant must list the source and complete one known-safe read-only task, such as the sample `orders` query from the deployment guide.
2. A principal from a different tenant must not see or query that source. The client should receive the documented not-found/access-denied result and no rows or internal details.
3. Run the selected-source connectivity check with the management token for the owner identity to confirm that a retained encrypted credential can be decrypted and the source is reachable.
4. Confirm the expected source identities, active and historical policy revisions, publication outcomes and audit records. The full metadata dump includes these tables; live query cursors and in-flight tasks are intentionally not recoverable.
5. Record negative-path evidence: `verify` with the wrong age identity or a damaged archive must fail; in a disposable project, removing a historical key still referenced by a saved source credential must make startup fail closed; unsupported schema origins must not be described as verified.
6. Repeat initialization and candidate startup migration, then confirm source counts, key IDs, policy history and audit outcomes have not changed unexpectedly. Interrupted MySQL DDL and old schema origins outside the current evidence remain unverified.

Record UTC time, candidate SHA, both image identities, MySQL version, archive checksum, key IDs, doctor outcome, source-check outcome, and redacted allowed/denied results. Do not attach `.env`, age identities, tokens, SQL dumps, raw source responses or unredacted logs to a public issue.

## Consistency, retention and limits

- The helper pauses this Compose project's API and MCP writers during the dump and restarts those that were active. MySQL `--single-transaction` gives a consistent InnoDB snapshot; it does not make MySQL DDL transactional or coordinate another application instance. Run one application version against the metadata database at a time.
- The encrypted backup protects the `.env` and metadata at rest. Keep the age identity separate, restrict backup-reader access, retain enough copies for the recovery objective, and periodically verify a copy from another host. Set retention according to the deployment's data-retention requirements; this project does not choose a universal retention period.
- Preserve every historical credential key required by stored rows. Losing a key cannot be repaired from the database dump. Key rotation must retain old and new keys until the selected-source checks pass.
- The current profile uses MySQL only. Other metadata backends, large databases, replication/failover, interrupted DDL, old upgrade origins, production-sized recovery times, business-source data and high availability need separate evidence.
- This is an operator-controlled recovery path, not an automatic rollback or a production support/SLA claim. Keep the original deployment intact until the fresh instance passes the checks above.
