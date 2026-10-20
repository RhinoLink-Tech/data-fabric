# Configuration Guide

## Overview

Data Fabric is configured primarily through a YAML configuration file located at `configs/config.yaml`. This document explains all available configuration options and their purposes.

## Main Configuration File

### Example Configuration (`configs/config.yaml`)

```yaml
server:
  port: "8080"
  mode: "debug"
  host: "0.0.0.0"
  read_header_timeout: "10s"
  read_timeout: "30s"
  write_timeout: "31m"
  idle_timeout: "120s"
  max_request_body_bytes: 1048576

database:
  host: "localhost"
  port: "3306"
  database: "gateway_db"
  username: "gateway_user"
  password: ""
  ssl: "false"

security:
  # Required for the HTTP API; keep this in a secret manager or environment.
  jwt_secret: ""
  jwt_expiration: "24h"
  rate_limit_per_minute: 60
  rate_limit_burst: 10
  enable_auth: true
  enable_rate_limit: true
  enforce_read_only: true

logging:
  level: "info"
  format: "json"

query:
  prefer_streaming: true
  execution_mode: "auto"

mcp:
  enabled: false
  transport: "stdio"
```

Copy `configs/config.yaml.example` to `configs/config.yaml` for local use. Keep real secrets out of source control.

## Configuration Sections

### Server Configuration

| Field | Type | Default |
|-------|------|---------|
| `server.port` | String | `8080` |
| `server.mode` | String | `debug` (`debug`, `test`, or `release`) |
| `server.host` | String | `0.0.0.0` |

`server.host` is the actual bind address of the HTTP listener (#127): an IP
address (IPv4 or bracket-escaped IPv6) or a hostname the listener resolves.
An empty value binds every interface, which keeps container deployments
working. An invalid or already-occupied address fails startup with the
address in the error.
| `server.read_header_timeout` | Duration | `10s` |
| `server.read_timeout` | Duration | `30s` |
| `server.write_timeout` | Duration | `31m` |
| `server.idle_timeout` | Duration | `120s` |
| `server.max_request_body_bytes` | Integer | `1048576` (1 MiB) |

The body limit returns HTTP 413 with error code `REQUEST_TOO_LARGE`. It wraps JSON request endpoints; fetch continuation uses GET and is unaffected.

### Database Configuration

| Field | Type | Default |
|-------|------|---------|
| `database.host` | String | `localhost` |
| `database.port` | String | `3306` |
| `database.database` | String | `gateway_db` |
| `database.username` | String | `gateway_user` |
| `database.password` | String | Empty |
| `database.ssl` | String | `false` |

`database.ssl` controls TLS for the metadata database connection (#143):
`false` (default) connects in plaintext, `true` requires TLS and verifies the
server certificate against the system roots and the connected hostname, and
`skip-verify` requires TLS without certificate verification for self-signed
servers. Any other value fails startup.

### Security Configuration

| Field | Type | Default |
|-------|------|---------|
| `security.jwt_secret` | String | None; required by the HTTP server |
| `security.jwt_expiration` | Duration | `24h` |
| `security.rate_limit_per_minute` | Integer | `60` |
| `security.rate_limit_burst` | Integer | `10` |
| `security.enable_auth` | Boolean | `true` |
| `security.enable_rate_limit` | Boolean | `true` |
| `security.enforce_read_only` | Boolean | `true` |

In `release` mode, authentication and read-only session enforcement cannot be disabled. The fetch API always requires a valid JWT, so the HTTP server requires a non-placeholder `security.jwt_secret` of at least 32 bytes in every mode.

### Logging, Query, and MCP Configuration

| Field | Type | Default |
|-------|------|---------|
| `logging.level` | String | `info` |
| `logging.format` | String | `json` |
| `query.prefer_streaming` | Boolean | `true` |
| `query.execution_mode` | String | `auto` (`auto`, `streaming`, or `pagination`) |
| `mcp.enabled` | Boolean | `false` |
| `mcp.transport` | String | `stdio` |

In `auto` mode, `type: 1` explicitly keeps a request on pagination. `type: 2`
requests streaming. Requests without a type use pagination for batches below
1,000 rows or SQL with an explicit result limit of 1,000 rows or fewer. Larger
unbounded batches use streaming only when `query.prefer_streaming` is true and
the registered source driver advertises streaming support. Unsupported drivers
fall back to pagination. If the stream concurrency limit is reached before the
query starts, auto mode also falls back; errors after execution begins are
returned without retrying the SQL.

The response includes `executionMode` with the engine that handled the request
(`streaming` or `pagination`). Explicit `query.execution_mode: streaming` and
`pagination` force the configured path; `auto` applies the selection rules
above.


## Data-source credential encryption

The HTTP and MCP servers require an encryption keyring at startup. Keys are injected by the runtime or a secrets manager; they are not stored in the repository or the YAML config. If either variable is missing, malformed, or does not include the active key, startup fails.

Generate a 32-byte key with openssl rand -base64 32 and store it in your secret manager. Inject the active key ID and a JSON map of key IDs to base64 keys:

    export DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=key-v1
    export DATA_FABRIC_CREDENTIAL_KEYS='{"key-v1":"<base64-encoded-32-byte-key>"}'

Use the same key across restarts. Generating a new key without retaining the old key makes previously encrypted data-source credentials unreadable.

At startup, Data Fabric encrypts legacy plaintext credentials and validates existing ciphertext in a database transaction before accepting requests. If migration fails, the transaction is rolled back and the server does not start. Data-source passwords and recognized secret values in additionalProps are encrypted before persistence and decrypted only when loaded for internal connection use.

### Key rotation

1. Keep the current key in the keyring and add a newly generated key under a new ID.
2. Set DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID to the new ID while retaining both keys in DATA_FABRIC_CREDENTIAL_KEYS.
3. Restart the service. Startup migrates all data-source credentials to the new key; any missing or invalid old key stops startup.
4. After the migration has completed successfully on every deployment, remove the old key from the injected keyring.

Back up the database and key material securely before rotation. Do not put actual key values in config files, shell scripts, or source control.

## Secret management

`configs/config.yaml` is a local, untracked file. Start from `configs/config.yaml.example` (placeholder values only) and keep real secrets out of it:

1. Environment variables override YAML settings. Inject DATABASE_USERNAME, DATABASE_PASSWORD, and SECURITY_JWT_SECRET (or the compatible JWT_SECRET alias) through a secret manager in every deployment.
2. The `.env` file is gitignored but **not loaded automatically**. To use one locally: `set -a; source .env; set +a`, or pass `--env-file` to `docker run` / `env_file` in compose.
3. In container deployments, inject secrets through the platform's secret mechanism (Kubernetes Secret, Docker secrets) instead of baking config files into images — the published image ships `config.yaml.example` only.
4. Calling `/api/v1/fetch` always needs a Bearer token, regardless of `security.enable_auth`. There is no token-issuing endpoint yet, so mint one yourself with the same `JWT_SECRET` (requires `pyjwt`):

   ```bash
   python3 -c "import jwt,time; print(jwt.encode({'sub':'dev','tenantId':'acme','username':'dev','roles':['admin'],'iat':int(time.time()),'exp':int(time.time())+86400}, 'YOUR_JWT_SECRET', algorithm='HS256'))"
   ```

   `tenantId` is optional: tokens without it belong to the default bucket (see [Data source authorization](#data-source-authorization)). If neither SECURITY_JWT_SECRET nor its JWT_SECRET compatibility alias is set, HTTP server startup fails; there is no built-in fallback secret.

## Local development vs production

| Setting | Local development (`debug`/`test`) | Production (`release`) |
|---|---|---|
| `security.enable_auth` | May be `false` to leave the data-source management API unauthenticated; `/api/v1/fetch` still requires a Bearer token either way (#6) | Must be `true`; the server refuses to start otherwise |
| `security.enforce_read_only` | May be `false` for engines that reject the read-only init | Must be `true`; the server refuses to start otherwise (#20) |
| `security.jwt_secret` | Required whenever the HTTP server runs: set, not a known placeholder, at least 32 bytes | Same |
| `server.mode` | `debug` / `test` | `release` |

Startup fails closed on insecure authentication settings before the metadata database is connected. `test_fetch_api.sh` reads the Bearer token from `FETCH_TOKEN` — mint one as shown under Secret management and export it before running the script.

## Data source authorization

Every stored data source carries an `ownerId` and a `tenantId`, stamped from the authenticated caller's JWT at creation time (the standard `sub` identity claim; the existing `userId` claim is accepted for compatibility, plus an optional `tenantId` claim). Tokens without a `tenantId` create rows in the default bucket — an empty tenant is never matched as a tenant, so tenantless callers only see their own rows. Rows created before ownership tracking (or while authentication is disabled) are visible only to administrators because their original owner cannot be established safely.

| Caller | Read (get, list, query, fetch) | Write (update, delete, activate, deactivate) |
|---|---|---|
| Admin (`roles` contains `admin`) | everything | everything |
| Owner (`sub` or compatibility `userId` matches `ownerId`) | own rows | own rows |
| Tenant member (`tenantId` matches) | the tenant's rows | no — read sharing only |
| Non-admin caller | not visible (admin only) | no |

Notable behaviors:

- **Uniform 403.** Non-admin callers get the same `403 DATASOURCE_ACCESS_DENIED` ("You do not have access to this data source") whether the data source is missing or belongs to someone else, on every management endpoint and on `/fetch`, so data source existence cannot be probed. Admins keep the distinct `404` for debugging.
- **Legacy rows.** Rows created before this change (or while `security.enable_auth` is disabled) have no owner or tenant. Only administrators can list, read, query, update, delete, activate, or deactivate these rows. A regular user cannot claim an unowned row by guessing its ID or adding request fields.
- **Per-tenant name uniqueness.** Two data sources may share a name across tenants but not within one tenant (or within the default bucket). The pre-upgrade globally unique name index is dropped automatically at startup.
- **Session binding.** Pagination and streaming sessions are bound to the identity that opened them; another user's valid Bearer token cannot continue someone else's session (`404 QUERY_SESSION_UNAVAILABLE`, indistinguishable from an unknown session).
- **Fleet endpoints are admin-only.** `/api/v1/database/health` and `/api/v1/database/connections/stats` enumerate every stored data source, so they require the `admin` role when `security.enable_auth` is on.
- **`security.enable_auth: false` boundary.** The switch only governs the data-source management API, where no identity exists to check: management requests then run in a legacy-equivalent mode, and created rows stay legacy. `/api/v1/fetch` is authenticated either way (#6), and its authorization checks still apply.

The service validates Bearer JWTs but does not provide a built-in login flow or token issuer. The deployment must issue tokens from a trusted identity source and protect the JWT signing secret: anyone who can sign tokens can assert a user ID, tenant, or role. Fine-grained permissions inside a tenant are out of scope.

## Environment Variables

Environment variables override YAML values. The canonical naming rule converts a config path to uppercase and replaces each dot with an underscore: `server.read_timeout` becomes `SERVER_READ_TIMEOUT`. No prefix is added.

Every service-config field supports this mapping:

| Configuration path | Canonical environment variable |
|--------------------|-------------------------------|
| `server.port` | `SERVER_PORT` |
| `server.mode` | `SERVER_MODE` |
| `server.host` | `SERVER_HOST` |
| `server.read_header_timeout` | `SERVER_READ_HEADER_TIMEOUT` |
| `server.read_timeout` | `SERVER_READ_TIMEOUT` |
| `server.write_timeout` | `SERVER_WRITE_TIMEOUT` |
| `server.idle_timeout` | `SERVER_IDLE_TIMEOUT` |
| `server.max_request_body_bytes` | `SERVER_MAX_REQUEST_BODY_BYTES` |
| `database.host` | `DATABASE_HOST` |
| `database.port` | `DATABASE_PORT` |
| `database.database` | `DATABASE_DATABASE` (`DATABASE_NAME` is a compatibility alias) |
| `database.username` | `DATABASE_USERNAME` |
| `database.password` | `DATABASE_PASSWORD` |
| `database.ssl` | `DATABASE_SSL` |
| `security.jwt_secret` | `SECURITY_JWT_SECRET` (`JWT_SECRET` is a compatibility alias) |
| `security.jwt_expiration` | `SECURITY_JWT_EXPIRATION` |
| `security.rate_limit_per_minute` | `SECURITY_RATE_LIMIT_PER_MINUTE` |
| `security.rate_limit_burst` | `SECURITY_RATE_LIMIT_BURST` |
| `security.enable_auth` | `SECURITY_ENABLE_AUTH` |
| `security.enable_rate_limit` | `SECURITY_ENABLE_RATE_LIMIT` |
| `security.enforce_read_only` | `SECURITY_ENFORCE_READ_ONLY` |
| `logging.level` | `LOGGING_LEVEL` |
| `logging.format` | `LOGGING_FORMAT` |
| `query.prefer_streaming` | `QUERY_PREFER_STREAMING` |
| `query.execution_mode` | `QUERY_EXECUTION_MODE` |
| `mcp.enabled` | `MCP_ENABLED` |
| `mcp.transport` | `MCP_TRANSPORT` |

When both a canonical name and its compatibility alias are set, the canonical name takes precedence. Durations use Go duration syntax, such as `30s` or `24h`; booleans use `true` or `false`.

Example:

```bash
export SERVER_PORT=8099
export DATABASE_HOST=postgres
export DATABASE_NAME=data_fabric
export DATABASE_USERNAME=data_fabric
export DATABASE_PASSWORD=secure-password
export JWT_SECRET="$(openssl rand -base64 32)"
```

`DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID` and `DATA_FABRIC_CREDENTIAL_KEYS` configure the separate data-source credential keyring documented above; they are not fields in the Viper service configuration.

## Configuration Validation and Schema Migration

Before opening the metadata database, startup validates the selected server mode, listener and database ports, the bind host, required database host/name/user, the `database.ssl` value, positive HTTP timeouts and request size, query execution mode, and the security requirements for HTTP transports. Database credentials are then used to open and ping the metadata database; a failed connection — or a bind address that cannot be listened on — prevents the service from accepting requests. HTTP and remote MCP also require a non-placeholder JWT secret of at least 32 bytes. Release mode requires authentication and read-only session enforcement. Invalid duration or scalar values that cannot be decoded into the typed configuration are reported during configuration loading.

For HTTP and MCP startup, Data Fabric applies the metadata schema migration before serving requests. The migration uses GORM AutoMigrate for the data-source schema and removes the legacy global name index; it is idempotent. A migration error is returned from startup and the process exits instead of serving against a partial schema.

### Production rollout

Treat metadata schema changes as part of the application release: back up the metadata database, test the release against a database copy, and ensure the startup database account has the DDL privileges needed for the migration. For a schema-changing upgrade, start one service replica first, verify migration and the health endpoint, then scale out the new version. Keep the previous application version available only when its code remains compatible with the migrated schema; schema changes are not automatically rolled back with an application rollback.

## Security Considerations

### Credential Management
- Store sensitive credentials in environment variables rather than config files
- Use secrets management systems in production
- Regularly rotate credentials
- Apply principle of least privilege

### Read-Only Access
- Data-source sessions are opened read-only where the engine supports it
  (`security.enforce_read_only`, #20); see
  [docs/readonly-access.md](readonly-access.md) for the engine support
  matrix, the per-data-source opt-out, and a least-privilege account
  cookbook
- Engines without a session read-only mode rely on SQL validation plus a
  least-privilege account — provision read-only credentials for every data
  source

### Production Settings
- Set server mode to `release` in production
- Use strong JWT secrets
- Configure appropriate rate limiting
- Enable secure logging practices
- Use TLS for all communications
