# DeepSeek function-calling reference client

This example connects a DeepSeek chat completion to Data Fabric over the
authenticated remote Streamable HTTP MCP endpoint. DeepSeek returns function
calls; this small local client validates arguments and invokes the corresponding
Data Fabric tools. The model API alone does not connect to MCP or access data.

If DeepSeek is used through Codex, Deep Code, or another MCP-capable host, connect
that host to Data Fabric and install the shared
[Data Fabric Agent Skill](../../skills/data-fabric-query/SKILL.md) instead.
This example is for applications that call the DeepSeek API directly.

## Prerequisites

- Python 3.11 or newer.
- A running Data Fabric deployment with remote MCP enabled and reachable over
  HTTPS.
- A bearer token accepted by that deployment and permission to access the
  intended data sources.
- A DeepSeek API key and a model that supports function/tool calls.
- The Data Fabric MCP server must expose list_data_sources, list_tables,
  describe_table, validate_sql_query, execute_sql_query, and fetch_next_batch.

## Install

Run these commands from the repository root:

~~~sh
python3 -m venv .venv-deepseek
. .venv-deepseek/bin/activate
python -m pip install --upgrade pip
python -m pip install -r examples/deepseek-agent/requirements.txt
~~~

## Configure credentials

Read credentials without echoing them or putting them in shell history:

~~~sh
read -rsp "DeepSeek API key: " DEEPSEEK_API_KEY; echo
export DEEPSEEK_API_KEY
read -rsp "Data Fabric bearer token: " DATA_FABRIC_TOKEN; echo
export DATA_FABRIC_TOKEN
export DATA_FABRIC_MCP_URL="https://<host>:8443/mcp"
export DEEPSEEK_MODEL="deepseek-flash"
~~~

The API key is sent only to https://api.deepseek.com. The Data Fabric bearer
token is sent only as the authorization header to the configured MCP endpoint.
Neither credential is included in prompts, function schemas, tool arguments, or
tool results. Use a certificate trusted by the operating system; do not disable
TLS verification for a self-signed certificate.

## Run

~~~sh
python examples/deepseek-agent/client.py
~~~

Example question:

> From the orders source, show the five highest value orders from the last 30 days.

The client discovers caller-visible sources and table metadata, asks DeepSeek to
draft SQL, calls Data Fabric validate_sql_query, and only then calls
execute_sql_query. It uses a fixed page size of 50 and a 60-second execution
timeout. Pagination is limited to four pages per query (at most 200 returned
rows), and the model can fetch further pages only as needed. If the page or
model-turn limit is reached, the answer is identified as partial.

The client uses a local result_id in model-visible function calls. The actual
Data Fabric query ID, slug, rotating token, and continuation URL remain in
local memory and are not sent to DeepSeek. Lost continuation responses invalidate
that local cursor; start a fresh query rather than replaying a possibly consumed
token. Large result pages are rejected with guidance to narrow the projection or
filters. These limits bound what this example returns and forwards; they do not
guarantee a low-cost backend scan.

The gateway remains responsible for authentication, source/table/column
authorization, read-only SQL validation, dialect handling, and query execution.
This reference client does not grant access or bypass server policies.

## Check

Run the bridge tests after installing the requirements:

~~~sh
cd examples/deepseek-agent
python -m unittest -v
python -m py_compile client.py test_client.py
~~~

## References

- [DeepSeek Tool Calls](https://api-docs.deepseek.com/guides/tool_calls/)
- [DeepSeek Chat Completions API](https://api-docs.deepseek.com/api/create-chat-completion/)
- [MCP Python SDK installation and client docs](https://py.sdk.modelcontextprotocol.io/)
- [Data Fabric MCP deployment](../../docs/mcp-deployment.md)
- [Data Fabric MCP tool reference](../../docs/mcp-tools.md)
