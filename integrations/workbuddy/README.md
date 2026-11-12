# WorkBuddy marketplace submission package

This package prepares a self-hosted Data Fabric MCP connector and a separate marketplace Skill. It is a submission candidate: publication, WorkBuddy platform acceptance, and the end-to-end client flow have not been verified by building these archives. The shared Codex skill remains the source of truth.

## Build

From the repository root, with Python 3:

```sh
python3 integrations/workbuddy/build_packages.py /tmp/data-fabric-workbuddy
python3 -m unittest discover -s integrations/workbuddy -p 'test_*.py'
```

The output directory contains `data-fabric-workbuddy-connector.zip` (configuration, icon and Skill) and `data-fabric-workbuddy-skill.zip` (a standalone Skill). The generator copies the shared `skills/data-fabric-query` and adds the WorkBuddy marketplace metadata only to the packaged copy. It includes the references and license; it does not package real credentials.

## Connection and authentication

The connector uses Streamable HTTP and the WorkBuddy user-supplied credential form. Each user provides the full HTTPS `/mcp` URL of their own deployment and an identity-provider-issued MCP access token. Use a certificate the client trusts, and ensure the client can reach the server. The token must satisfy the gateway's configured issuer, audience and scope; replace it in the connector when expired. The management REST API JWT is a different credential. See [MCP deployment](../../docs/mcp-deployment.md) and [client setup](../../docs/agent-skill-zh.md).

The package uses `auth_mode: token`; this is a manually supplied OIDC access token, not an automated OAuth flow or a long-lived Data Fabric API key. WorkBuddy documents local storage for this mode. A tenant's own policies govern where its service and identity provider run. Never include tokens, keys, or production data in a package or screenshot.

The connector uses bilingual name and examples, so `minWorkbuddyVersion` is 4.24.0. `source: data-fabric` must be checked for global availability on the platform. Change the source and matching MCP server key if the platform rejects it. The skill's version and connector version are currently 1.0.0; update both intentionally for new marketplace submissions.

## Before submitting

1. On the WorkBuddy open platform, verify account eligibility, identifier availability, required metadata, and the current import/upload format. Follow [connector](https://open.workbuddy.cn/docs/connector) and [skill](https://open.workbuddy.cn/docs/skill) documentation; the platform is the authority for acceptance. The ZIP is a candidate, not evidence of approval.
2. In WorkBuddy, install the candidate with a reachable **isolated synthetic** Data Fabric profile and a dedicated authorized identity. Complete connection and trust setup without bypassing certificate checks. Verify the connector shows the expected tools, then inspect the sample schema, validate and run a read-only query and fetch its continuation. The reference scenario and expected IDs are in the [client guide](../../docs/agent-skill-zh.md#用合成订单验收).
3. Check invalid/expired token, denied source/table, inaccessible server, and untrusted certificate behavior; never disclose tokens in results. Record WorkBuddy version, gateway version, scenario and observed results. A standalone skill install does not configure a connection.
4. Submit the connector for platform review; publish only after approval. Submit the standalone Skill separately if a separate Skill-market listing is desired. Review platform presentation and dependencies before release.

Existing WorkBuddy native connector and query validation is tracked in [#504](https://github.com/RhinoLink-Tech/data-fabric/issues/504); this packaging does not close that issue.
