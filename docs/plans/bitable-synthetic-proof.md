# Synthetic Bitable-style read proof

This is an internal, local-only experiment for #836, related to #755. It is
not a registered data source, a Feishu connector, an MCP tool, or a SQL
interface. Its synthetic HTTP schema is deliberately not the Feishu API.

A numeric loopback HTTP endpoint at `/records` returns synthetic records
containing `record_id`, optional `status` and optional `created_at`, along
with `has_more` and a fake provider `page_token`. Unknown fields are ignored.
The fake request carries a fixed table and view and the requested page size.
Absent fields remain unknown. No credentials are accepted.

The reader binds each opaque single-use continuation to caller, tenant, source,
table and view and enforces expiration and configured page, row, response-byte,
result-byte, time and active-session limits. It never returns the provider
cursor. Redirects, proxies and non-loopback endpoints are disallowed. Provider
errors are reduced to bounded error categories. An incomplete or truncated
read must not be presented as a complete summary. `complete` means only that
the fake provider indicated the end of pagination; it does not establish a
consistent snapshot if records change between pages.

Scope values in this experiment are supplied by the caller and do not prove
authentication or authorization. Tokens live in one process and do not survive
restart or work across replicas. Before any real integration, establish and
verify provider authentication, authenticated identity and source access,
provider-specific field semantics, cursor behavior, credentials, and the real
API contract, as tracked in #755.

Run the isolated checks without containers or network access outside loopback:

```sh
go test -race ./internal/bitableproof
```
