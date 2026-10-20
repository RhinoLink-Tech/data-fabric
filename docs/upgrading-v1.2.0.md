# Upgrading from v1.2.0 to v2.0.0

**Status:** Candidate instructions. Apply only after a stable v2.0.0 release is published.

The latest published stable release is v1.2.0. This guide only covers upgrading from v1.2.0; v1.0.0, v1.1.0, untagged snapshots, and custom builds need a separate rehearsal before upgrading. The security-maintenance candidate in #206 is not a published release.

## Before upgrading

1. Schedule a maintenance window and stop the old Data Fabric process. Do not run old and new binaries against the metadata database at the same time.
2. Back up the metadata database and verify that the backup can be restored. The tested MySQL path is documented in [the D15 rehearsal](operations/mysql-upgrade-rehearsal.md). MySQL DDL may commit implicitly.
3. Back up the local configs/config.yaml securely before updating the checkout. The tracked file was removed; a pull may delete it. Do not commit or share the backup.
4. Inventory existing data-source rows and preserve all source connection credentials. Legacy rows without owner/tenant metadata become admin-only; the migration does not infer their owner.
5. Preserve the credential-encryption keyring. Do not generate a replacement key for an existing deployment unless performing the documented key-rotation procedure with both old and new keys present.

## Prepare the new configuration

Start from configs/config.yaml.example and move real values into the deployment's secret manager. At minimum, provide:

- DATABASE_USERNAME and DATABASE_PASSWORD for the metadata database.
- SECURITY_JWT_SECRET (or the compatible JWT_SECRET alias), a random value of at least 32 bytes.
- DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID and DATA_FABRIC_CREDENTIAL_KEYS, retaining every key needed to decrypt existing values.
- security.enable_auth: true and server.mode: release for production.

The example file documents these settings. Keep generated keys and tokens out of source control, shell history, issue comments, and deployment logs.

## Upgrade

1. Deploy the new binary or image with the backed-up metadata database and persistent secrets.
2. Start one instance and wait for the credential and schema migrations to finish before routing traffic. Startup fails closed if a required key is missing or invalid.
3. Confirm health, then confirm an administrator can list legacy data sources. Verify ordinary users can access only rows allowed by their owner/tenant claims.
4. Update clients to send a valid Bearer JWT on both the initial and continuation /api/v1/fetch requests. A disabled management-auth switch does not disable fetch authentication.
5. Smoke-test one read-only query and result continuation against each connector your deployment uses. Check logs and readiness before restoring traffic.

### Behavior changes to verify

- Bearer auth is required for /api/v1/fetch regardless of security.enable_auth.
- No default JWT secret exists; startup requires a valid configured secret.
- Legacy data sources without ownership metadata are available to administrators only.
- Source names can repeat across tenants, but duplicates within one tenant are rejected.
- Credentials are encrypted at rest and require the deployment keyring on every restart.

## Recovery

If startup or smoke checks fail, stop the new version. Restore the metadata database snapshot, the previous configuration, and the exact secret material used by the old deployment, then redeploy the old version.

Do not point the old binary at a partially migrated database or attempt an in-place downgrade. The rehearsed restore path is limited to MySQL 8.0.46 with synthetic data; other metadata database engines and production-sized datasets need their own rehearsal. The application does not modify the source databases' business data during the metadata migration.

## Release limitation

Do not use these candidate instructions as evidence that a stable release is available. The P0 credential-rotation gate in #12 and the selected runtime/capability gates in the release review must be closed first.
