# Data Fabric Agent Skill

The canonical, provider-neutral bundle is
[`skills/data-fabric-query`](../skills/data-fabric-query/SKILL.md). It follows
the [Agent Skills format](https://agentskills.io/specification) and teaches a
compatible agent client to discover sources, resolve source ambiguity,
inspect schema, validate read-only SQL, and retrieve bounded result batches.

## Prepare the connection

[中文接入指南](agent-skill-zh.md)

Use a reachable HTTPS Streamable HTTP endpoint ending in `/mcp`, a data
source the caller can read, and an access token issued by the deployment's
OIDC provider. The gateway checks the configured issuer, audience, and
scopes, including `data-fabric.query` by default. Trust the gateway's
certificate in the client and obtain a fresh token from the provider when
it expires. Use the provider's JWT access token and reserve the management
token for the REST API. See the
[MCP deployment guide](mcp-deployment.md#5-prepare-management-and-mcp-access-tokens).

Obtain the repository checkout or a release source archive. Install the
complete `skills/data-fabric-query/` directory with its `references/`
directory and license. For other Agent Skills hosts, use the host's skill
installer and connect Data Fabric MCP or an equivalent tool adapter.

## Codex

1. Copy `data-fabric-query/` into `.agents/skills/` in the project where you
   want to use it. The resulting path is
   `.agents/skills/data-fabric-query/SKILL.md`.
2. Set `MCP_ACCESS_TOKEN` in the environment that launches Codex. Update the
   URL in the [Codex example](../examples/mcp-clients/codex.config.toml) and
   add that block to your Codex `config.toml`, or register the same server
   using the CLI.

   ```sh
   codex mcp add data-fabric --url https://data-fabric.example.com/mcp --bearer-token-env-var MCP_ACCESS_TOKEN
   ```

3. Start a new session in the project. Use `/mcp` to check the tools, then
   invoke `$data-fabric-query` with a data question. Review any tool approval
   prompts for the sample read-only calls.

See [Codex Skills](https://learn.chatgpt.com/docs/build-skills) and
[Codex MCP](https://learn.chatgpt.com/docs/extend/mcp).

## WorkBuddy

1. From the Data Fabric repository root, package the Skill. The ZIP has
   `data-fabric-query/` as its top-level folder.

   ```sh
   python3 -m zipfile -c data-fabric-query.zip skills/data-fabric-query
   ```

2. Open **Skills → Add Skill → Upload Skill → Select ZIP file** and choose
   the bundle. When WorkBuddy reports that the Skill is safe, select
   **Continue installation** if prompted. Confirm that `data-fabric-query`
   appears among the installed skills.
3. In **Connectors → Custom Connector**, configure `streamableHttp` and
   your `/mcp` URL using the
   [WorkBuddy example](../examples/mcp-clients/workbuddy.mcp.json). Supply the
   `Authorization` header as `Bearer <OIDC_ACCESS_TOKEN>` in the local
   connector configuration. Verify the connection and enable it.
4. In a new chat, select or explicitly request `data-fabric-query` and ask
   your data question.

See WorkBuddy's [Skill upload](https://intl.cloud.tencent.com/zh/document/product/1316/84503),
[custom connector](https://intl.cloud.tencent.com/zh/document/product/1316/84494),
and [MCP configuration](https://open.workbuddy.cn/docs/connector) guides.

For applications calling the DeepSeek API directly, the
[DeepSeek function-calling example](../examples/deepseek-agent/README.md)
maps model function calls to the same authenticated MCP tools.

## Bundle contents and limits

- `SKILL.md` contains the shared workflow and standard `name`/`description`
  frontmatter, plus the standard license field.
- `references/tools.md` documents capability mapping, argument/response
  names, validation failures, and continuation-token handling.
- `references/sql-and-examples.md` contains SQL boundaries and worked
  examples. Read these when using the corresponding operation.
- `LICENSE.txt` carries the repository's Apache-2.0 license with the portable
  bundle.

The bundle is self-contained and can be moved independently of the checkout.
It does not register data sources, grant access, install a server, or broaden
the server's supported SQL subset. Result size is bounded per batch; that
does not guarantee a cheap backend scan. Additional pages are fetched only
when needed, and partial results must be identified as such.

## Verify with synthetic orders

Start an isolated MySQL sample deployment and register `sample-mysql` using
the [MCP deployment guide](mcp-deployment.md#quickstart). Grant the test caller read
access to `sample.orders`. Run the same task in each client.

> Use data-fabric-query with the connected Data Fabric MCP tools. Find the
> sample MySQL source and inspect the orders table and its id column.
> Validate the read-only SQL, then return all order IDs in ascending order.
> Set batch_size to 2 for both execution and continuation. Report the IDs
> and whether the result is complete.

Check the tool calls for source discovery, table listing, column inspection,
successful SQL validation, execution, and continuation. Expect `[1, 2]`
with a `nextUri`, then `[3]` with an empty or absent `nextUri`. The final
answer should report `[1, 2, 3]` as complete. Record the client version,
gateway image version, and row count per batch.

If tools are missing, check that the connector is enabled, the URL ends in
`/mcp`, and the client trusts the certificate. For a 401, check the token
expiry, issuer, audience, and scope. If the connection works but the sample
is absent, check the caller's read grant. See the
[MCP tool reference](mcp-tools.md) for other errors.

## Validate changes

Use Python 3.11 or newer in a virtual environment, from the repository root:

```sh
python3 -m venv /tmp/data-fabric-skill-check
/tmp/data-fabric-skill-check/bin/python -m pip install -r requirements-agent-skills.txt
/tmp/data-fabric-skill-check/bin/skills-ref validate skills/data-fabric-query
```

The dependency file pins the official
[`skills-ref` reference validator](https://github.com/agentskills/agentskills/tree/main/skills-ref)
to a specific upstream commit. CI runs the same format validation. This is a
development check, not a dependency of the running Data Fabric server.

Format validation does not prove workflow correctness. When changing the
bundle, also check its references and examples against the current tool
schemas and exercise missing tools, ambiguous sources, `valid: false`, and
partial/expired pagination with a test adapter or synthetic tool results.
Use synthetic values; never put deployment secrets or source data in fixtures.
