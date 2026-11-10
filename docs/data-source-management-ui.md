# Data-source Management UI

中文指南：[数据源管理界面与首次任务](data-source-management-ui-zh.md)。

Data Fabric serves a self-hosted management page at /ui/. The page is embedded in the server binary and calls the existing same-origin management APIs. It supports listing, creating, editing, testing, activating, deactivating, and deleting data sources.

## Access and authentication

Open the HTTPS address for your deployment followed by /ui/ (or the corresponding local HTTP URL). When management authentication is enabled, paste an existing Bearer JWT into the page. The API continues to enforce its normal owner, tenant, and administrator access rules; the UI does not issue tokens or add an authentication bypass. A token is held only in the current page and is cleared when the page is closed or refreshed.

For production, keep security.enable_auth set to true, serve the page through HTTPS, and use the deployment's normal network access controls. The HTML, JavaScript, and CSS are served from the same Data Fabric origin with a restrictive Content Security Policy and Cache-Control: no-store.

## Configure a source

1. Select Add source and choose a connector shown as registered and supported by the server's support catalog.
2. Enter the common connection fields. Connector-specific fields can be supplied as a JSON object under Additional properties.
3. Use Test connection before saving. The connection test uses the entered configuration and does not save it.
4. For an existing source, the server never returns its stored password or sensitive additional properties. Leave the password blank to retain it; enter a replacement value to change it. If a connection test needs the existing password, enter it again for that test.
5. Use the row actions to edit, activate or deactivate, and delete a source. Deletion requires a browser confirmation.

The form is intentionally generic. Some connectors require connector-specific configuration that must be provided through Additional properties; the UI does not expose schema browsing or query editing.

## Complete the first authorized query

1. Choose the intended owner and tenant identity before registering the source. Use a least-privilege, read-only database account. Test the connection, save the source, and confirm that it is active. The connection test does not register the source.
2. Connect a remote MCP client to the endpoint from the deployment guide. For Claude Code, set an OIDC access token issued for the configured MCP audience and scope, then run:

   ```bash
   export MCP_HOST=localhost
   : "${MCP_ACCESS_TOKEN:?Set an OIDC access token first}"
   claude mcp add --transport http data-fabric "https://$MCP_HOST:8443/mcp" --header "Authorization: Bearer $MCP_ACCESS_TOKEN"
   ```

   Replace the host and port with the deployed MCP endpoint. The client stores the header in its local MCP configuration; protect that file and renew the token through the identity provider when it expires. The management JWT entered in the browser is a separate credential and must not be reused as an MCP token. For a local self-signed certificate, follow the deployment guide to set NODE_EXTRA_CA_CERTS; keep TLS verification enabled.
3. In the MCP client, call list_data_sources, list_tables, and describe_table under the intended principal, then call execute_sql_query with one bounded, read-only query using only visible tables and columns. Use the documented pagination or streaming mode that fits the task. For the synthetic local fixture, the sample is SELECT customer, amount FROM orders ORDER BY amount DESC.
4. Connect a second application with its own OIDC principal mapped to the approved tenant. It should reuse the registered source and policy without receiving the source credential. Repeat the authorized task, then use a principal from an unauthorized tenant to confirm it cannot read the source or rows. Where table or column policies are configured, confirm restricted objects stay hidden or are denied.

Run the same task for at least two operators unfamiliar with repository internals, in the same environment, with a baseline artifact and the candidate artifact. Record each artifact tag/digest, client version, active and elapsed time, failed attempts, expected and actual outcomes, and maintainer interventions. Label synthetic rehearsal evidence separately from model quality, real-team adoption, and customer value. Use synthetic or explicitly approved data; keep secrets and raw records out of the notes. Follow the [independent first-use rehearsal protocol](validation/independent-first-use.md) to lock artifacts, control image transfers, and record each run.

## Safe recovery and correlation IDs

| Failure | Safe next action |
| --- | --- |
| Connection test fails before save | Check that the source host resolves and is reachable from the API container, then check its port, TLS trust, and credentials. Keep the source unregistered until the connection is healthy. |
| MCP returns 401 or the session has expired | Issue a fresh access token from the configured identity provider for the required issuer, audience, and scope, then reconnect the MCP client. The management JWT is not an MCP token. |
| A source is missing or access is denied | Check that the source is active and that the OIDC subject and tenant mapping match the intended owner and approved policy. Ask an administrator for the narrow policy change needed; do not switch to an admin identity to bypass access checks. |
| A table or column is missing or denied | Treat the result as policy filtering. Ask the policy owner to verify the intended table and column grants, then retry only with objects visible to that principal. |
| A bounded query reports a source error | Check the connector health and retry with a read-only query using a visible table and column. Use local service logs to diagnose the underlying source failure; do not include credentials or raw rows in reports. |

Management REST error bodies include a top-level correlationId, and HTTP requests expose the X-Correlation-ID response header. Keep the ID with the UTC time, API or MCP tool, and sanitized error text. Some MCP clients hide response headers; if the client does not show an ID, record the request time and ask the deployment operator to match it in local logs. For the self-hosted Compose profile, inspect logs locally:

    docker compose logs --since=10m data-fabric-api data-fabric-mcp

Never share authorization headers, tokens, source configuration, or raw query results in an issue. Query cursors belong to one process and are lost when that process restarts.

This page manages data sources only. Schema discovery, query execution, tenant isolation, and result continuation use the existing MCP tools and authorization contracts; the UI does not add a schema or query API.

## Pilot status

This interface is an engineering reference implementation. Reduced setup time or operator effort has not been established with a real team. Validate those outcomes using the [M4 pilot evidence protocol](validation/real-team-pilot-evidence.md) before making a customer-value claim.
