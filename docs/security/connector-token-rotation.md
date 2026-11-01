# Connector access-token rotation

The connection pool's token manager rotates credentials only for drivers that
explicitly declare the RequiresTokenRotation capability. The current provider
is Google OAuth for BigQuery. It accepts service-account or authorized-user
credential JSON in a data source's additionalProps.credentials_json; when
that value is absent, it uses Google Application Default Credentials.

Credential JSON is stored through the data-source repository, which encrypts
sensitive properties at rest with the configured credential vault. A provider
must return a non-empty access token with a future expiry before the manager
attempts to persist it. The access token and its expiry are then written
together in one database transaction. The repository compares the previous
token before replacement, so concurrent gateway instances cannot overwrite a
newer refresh result with a stale one. The old token remains available if
provider refresh or persistence fails.

The server starts the rotation checker with its connection pool and stops it
when the pool closes. A data source is registered when its connection is initialized. If it has
no valid stored token, or the token is within five minutes of expiry, the
connection setup requests and persists a replacement before proceeding. The
background checker also checks registered tokens every five minutes.
Explicit refresh requests fail with an unsupported error if no provider is
registered; the service does not return fabricated token values.

This lifecycle manages credentials; it does not add query execution support to
a data-source driver. Each driver must separately provide a working query path
and declare token-rotation capability only when its authentication uses this
manager.
