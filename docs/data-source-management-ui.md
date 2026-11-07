# Data-source Management UI

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

## Pilot status

This interface is an engineering reference implementation. Reduced setup time or operator effort has not been established with a real team. Validate those outcomes using the [M4 pilot evidence protocol](../validation/real-team-pilot-evidence.md) before making a customer-value claim.
