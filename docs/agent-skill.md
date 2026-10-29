# Data Fabric Agent Skill

The canonical, provider-neutral bundle is
[`skills/data-fabric-query`](../skills/data-fabric-query/SKILL.md). It follows
the [Agent Skills format](https://agentskills.io/specification) and teaches a
compatible agent client to discover sources, resolve source ambiguity,
inspect schema, validate read-only SQL, and retrieve bounded result batches.

## Install and connect

1. Obtain a checkout or release archive of this repository.
2. Import or copy the complete `skills/data-fabric-query/` directory using
   the client's supported Agent Skills installation mechanism. Preserve the
   folder name and its `references/` directory. Do not copy only `SKILL.md`.
3. Connect the client to a configured Data Fabric MCP server, or an adapter
   providing the equivalent capabilities. See the [MCP deployment guide](mcp-deployment.md)
   for server setup and the [tool reference](mcp-tools.md) for its public API.
   Use the deployment's existing authentication configuration; the bundle
   does not configure a connection or contain database credentials.
4. Ask a question against an identified source, for example: "Use our orders
   source to show the five largest orders. Inspect the available schema
   first." If the source name is ambiguous, the skill asks for a choice.

Client discovery paths and activation controls differ, so this bundle does
not prescribe a client-specific configuration file, tool namespace, model,
or installation command. A model API alone does not load skills or execute
tools: it needs an agent client with skill and tool support.

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
