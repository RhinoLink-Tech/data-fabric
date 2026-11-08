# Public gateway acceptance

Run the deterministic [A01–A26 matrix](../plans/gateway-delivery.md#public-task-acceptance-matrix) from the repository root with Docker and Docker Compose v2 available.

```sh
go run ./deploy/mcp/reference/cmd/acceptance > public-acceptance.jsonl
```

The command starts the disposable [reference deployment](../mcp-reference.md), checks its baseline connectivity, then exercises the public REST API and the MCP Streamable HTTP endpoint. It uses synthetic MySQL and PostgreSQL sources, an owner and a reader in one tenant, a reader in a different tenant, an operator for audit reads, and a local TLS OIDC issuer. No external identity provider or model service is needed.

The acceptance JSONL on stdout contains one record per case and transport with the case ID, transport, result, duration in milliseconds, and a safe reason for any failure or unsupported case. Stderr identifies the profile and query settings, checkout revision, pinned database images, gateway image ID, setup timings and container memory. Keep both streams together when collecting evidence. A failed setup or any failed case exits nonzero.

```sh
go run ./deploy/mcp/reference/cmd/acceptance -smoke
```

The smoke subset runs A01, A05, A11, A12, A17, A19, A23, A25 and A26 on both transports, with A25 REST recorded as unsupported. It uses an eight-second query-session TTL and tenant capacity of two so expiry and timeout outcomes finish in a bounded run. A26 verifies that a three-page REST or MCP result produces one logical success, three successful batches and three returned rows. Every JSONL record includes the candidate SHA, dirty-tree state, gateway image reference and ID, acceptance profile, and an explicit pass/fail/unsupported result. Use the full command for the complete A01–A26 delivery matrix. The `-repo` flag selects a checkout and `-image` uses a prebuilt local image. When using `-image` for evidence, build the image from the revision under test and retain the reported image provenance. The default run removes the isolated stack and its generated credentials. Use `-keep` only for local investigation.

## Oracles

| Cases | Evidence |
|---|---|
| A01–A04 | Owner and same-tenant reader discover the same two stable IDs. The other tenant discovers neither. Source detail exposes connection metadata without credentials. MCP table discovery returns only `orders` and `order_items`, and the visible `orders` schema excludes `customer`. |
| A05–A07 | A bound `shipped` returns IDs 1 and 3. An injection-shaped bound string returns zero rows. IDs are JSON integers and the discounts are JSON null, `"1.25"`, and `"0.00"`. The filtered join counts two items for order 1. |
| A08–A11 | Two interleaved sessions return their exact ordered rows. An old token fails after rotation. Two requests using the same token yield one advancing batch and one rejection. The full run uses an eight-second session TTL and two query slots per tenant. After filling both slots, expiry releases both slots before any retry with an expired token. |
| A12–A17 | Requests with absent, expired, or invalid-signature bearers are rejected before data access. MCP also rejects the wrong issuer, wrong audience, and absent scope. The other tenant receives neither source detail nor rows. A13 verifies its fixture bearers stay out of gateway logs. |
| A18–A21 | The denied table and column references fail, including projection, filter, join and sort references. Writes, side-effecting functions and malformed SQL produce stable errors without raw SQL or connector details. |
| A22–A24 | Stopping the synthetic MySQL service produces a safe connector failure or request timeout while PostgreSQL still returns the expected rows. A caller from the other tenant remains denied during the outage. MySQL is restarted and queried again. An exclusive lock on the synthetic PostgreSQL table triggers a one-second query deadline; the logical-query timeout counter increases once per request, followed by full query capacity. The operator can correlate a successful, denied and failed query per transport without seeing tokens, raw SQL or result rows in audit events. |
| A26 | REST and MCP each fetch the same three-row fixture over three batches. The logical outcome increases once, accumulated row and batch counters each increase by three, and the existing initial-request metrics retain their meaning. |

REST records A03 and A04 as unsupported because it has no corresponding policy-filtered table metadata endpoints. It records A14–A16 as unsupported because REST authenticates using HS256 and the OIDC checks apply to MCP. A18 checks query denial in both transports and table description denial in MCP.

The suite makes fault injections only within its own Compose project. Run it against synthetic fixtures. The result describes the pinned connectors and two public transports exercised by this reference environment. The current matrix covers static interleaved sessions and token rotation; it does not prove cross-page snapshot semantics under source mutations. See [query result semantics](../query-result-semantics.md) and issue #455 for that separate contract.
