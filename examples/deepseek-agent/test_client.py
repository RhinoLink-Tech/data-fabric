import json
import unittest
from types import SimpleNamespace

from client import (
    Cursor,
    DataFabricFunctions,
    MAX_PAGE_COUNT,
    run_agent,
    validate_arguments,
    validate_mcp_url,
)


def result(payload, is_error=False):
    return SimpleNamespace(
        structuredContent=None,
        isError=is_error,
        content=[SimpleNamespace(text=json.dumps(payload))],
    )


class FakeSession:
    def __init__(self, responses):
        self.responses = list(responses)
        self.calls = []

    async def call_tool(self, name, arguments):
        self.calls.append((name, arguments))
        return self.responses.pop(0)

    async def list_tools(self):
        return SimpleNamespace(tools=[SimpleNamespace(name=name) for name in (
            "list_data_sources",
            "list_tables",
            "describe_table",
            "validate_sql_query",
            "execute_sql_query",
            "fetch_next_batch",
        )])


class InputValidationTests(unittest.TestCase):
    def test_mcp_url_requires_https_without_embedded_credentials(self):
        self.assertEqual(
            validate_mcp_url("https://gateway.example/mcp"),
            "https://gateway.example/mcp",
        )
        for url in (
            "http://gateway.example/mcp",
            "https://user:password@gateway.example/mcp",
            "https://gateway.example/mcp?token=secret",
            "https://gateway.example/mcp#fragment",
        ):
            with self.subTest(url=url), self.assertRaises(ValueError):
                validate_mcp_url(url)

    def test_rejects_extra_arguments(self):
        with self.assertRaises(ValueError):
            validate_arguments("list_data_sources", {"token": "secret"})

    def test_rejects_nested_query_parameters(self):
        with self.assertRaises(ValueError):
            validate_arguments(
                "execute_sql_query",
                {"datasource_id": "ds", "sql": "select 1", "parameters": [{"secret": "value"}]},
            )

    def test_rejects_non_finite_parameter_values(self):
        with self.assertRaises(ValueError):
            validate_arguments(
                "execute_sql_query",
                {"datasource_id": "ds", "sql": "select 1", "parameters": [float("inf")]},
            )

    def test_rejects_oversized_parameter_arrays(self):
        with self.assertRaises(ValueError):
            validate_arguments(
                "execute_sql_query",
                {"datasource_id": "ds", "sql": "select 1", "parameters": list(range(65))},
            )

    def test_mcp_url_rejects_invalid_ports(self):
        invalid_urls = [
            "https://gateway.example:invalid/mcp",
            "https://gateway.example:65536/mcp",
            "https://gateway.example:0/mcp",
        ]

        for url in invalid_urls:
            with self.subTest(url=url):
                with self.assertRaises(ValueError):
                    validate_mcp_url(url)

    def test_mcp_url_accepts_valid_ports(self):
        valid_urls = [
            "https://gateway.example/mcp",
            "https://gateway.example:1/mcp",
            "https://gateway.example:443/mcp",
            "https://gateway.example:65535/mcp",
            "https://[::1]:443/mcp",
        ]

        for url in valid_urls:
            with self.subTest(url=url):
                self.assertEqual(validate_mcp_url(url), url)


class FunctionBridgeTests(unittest.IsolatedAsyncioTestCase):
    async def test_execution_validates_and_hides_server_cursor(self):
        session = FakeSession([
            result({"valid": True, "sql": "select id from orders"}),
            result({
                "queryId": "internal-query",
                "slug": "internal-slug",
                "token": "internal-token",
                "nextUri": "/fetch/internal-token",
                "columns": [{"name": "id", "type": "int"}],
                "entries": [[1]],
                "totalCount": 2,
            }),
            result({
                "queryId": "internal-query",
                "slug": "internal-slug",
                "token": "rotated-token",
                "nextUri": "",
                "columns": [{"name": "id", "type": "int"}],
                "entries": [[2]],
                "totalCount": 2,
            }),
        ])
        bridge = DataFabricFunctions(session, secrets=("model-secret", "data-token"))

        first = await bridge.invoke(
            "execute_sql_query",
            {"datasource_id": "ds-1", "sql": "select id from orders"},
        )
        first_text = json.dumps(first)
        self.assertEqual(session.calls[0][0], "validate_sql_query")
        self.assertEqual(session.calls[1][0], "execute_sql_query")
        self.assertEqual(session.calls[1][1]["batch_size"], 50)
        self.assertEqual(session.calls[1][1]["timeout"], 60)
        self.assertNotIn("internal-query", first_text)
        self.assertNotIn("internal-slug", first_text)
        self.assertNotIn("internal-token", first_text)
        self.assertNotIn("model-secret", first_text)
        self.assertTrue(first["has_more"])
        result_id = first["result_id"]

        second = await bridge.invoke("fetch_next_batch", {"result_id": result_id})
        self.assertEqual(
            session.calls[2],
            (
                "fetch_next_batch",
                {
                    "query_id": "internal-query",
                    "slug": "internal-slug",
                    "token": "internal-token",
                    "batch_size": 50,
                },
            ),
        )
        self.assertNotIn("rotated-token", json.dumps(second))
        self.assertFalse(second["has_more"])
        self.assertNotIn(result_id, bridge.cursors)

    async def test_invalid_sql_is_never_executed(self):
        session = FakeSession([result({"valid": False, "error": "Only read-only SQL is allowed."})])
        bridge = DataFabricFunctions(session)

        output = await bridge.invoke(
            "execute_sql_query",
            {"datasource_id": "ds-1", "sql": "delete from orders"},
        )

        self.assertFalse(output["executed"])
        self.assertEqual([call[0] for call in session.calls], ["validate_sql_query"])

    async def test_tool_errors_are_returned_without_known_secrets(self):
        session = FakeSession([result({"message": "access denied data-token"}, is_error=True)])
        bridge = DataFabricFunctions(session, secrets=("data-token",))

        output = await bridge.invoke("list_tables", {"datasource_id": "ds-1"})

        self.assertIn("access denied", output["error"])
        self.assertNotIn("data-token", json.dumps(output))

    async def test_pagination_stops_at_local_page_limit(self):
        session = FakeSession([])
        bridge = DataFabricFunctions(session)
        bridge.cursors["result_1"] = Cursor("query", "slug", "token", MAX_PAGE_COUNT)

        output = await bridge.invoke("fetch_next_batch", {"result_id": "result_1"})

        self.assertTrue(output["partial"])
        self.assertTrue(output["has_more"])
        self.assertIn("page limit reached", output["error"])
        self.assertEqual(session.calls, [])

    async def test_oversized_page_is_not_forwarded_to_the_model(self):
        session = FakeSession([
            result({
                "queryId": "internal-query",
                "slug": "internal-slug",
                "token": "internal-token",
                "nextUri": "/fetch/next",
                "columns": [{"name": "value", "type": "string"}],
                "entries": [["x" * 50_100]],
                "totalCount": 1,
            })
        ])
        bridge = DataFabricFunctions(session)

        output = await bridge.invoke("list_tables", {"datasource_id": "ds-1"})

        self.assertIn("too large", output["error"])
        self.assertNotIn("x" * 100, json.dumps(output))
        self.assertEqual(bridge.cursors, {})

    async def test_source_listing_only_exposes_allowlisted_fields(self):
        session = FakeSession([
            result({
                "data_sources": [{
                    "id": "ds-1",
                    "name": "orders",
                    "type": "postgres",
                    "status": "active",
                    "host": "private.internal",
                    "password": "must-not-appear",
                }],
                "count": 1,
            })
        ])
        bridge = DataFabricFunctions(session)

        output = await bridge.invoke("list_data_sources", {})

        self.assertEqual(
            output,
            {"data_sources": [{"id": "ds-1", "name": "orders", "type": "postgres", "status": "active"}], "count": 1},
        )
        self.assertNotIn("private.internal", json.dumps(output))
        self.assertNotIn("must-not-appear", json.dumps(output))


class FakeMessage:
    def __init__(self, content=None, tool_calls=None):
        self.content = content
        self.tool_calls = tool_calls or []

    def model_dump(self, exclude_none=True):
        if self.tool_calls:
            return {
                "role": "assistant",
                "tool_calls": [
                    {
                        "id": call.id,
                        "type": "function",
                        "function": {
                            "name": call.function.name,
                            "arguments": call.function.arguments,
                        },
                    }
                    for call in self.tool_calls
                ],
            }
        return {"role": "assistant", "content": self.content}


class AgentLoopTests(unittest.IsolatedAsyncioTestCase):
    async def test_model_function_call_runs_and_returns_tool_result(self):
        call = SimpleNamespace(
            id="call-1",
            function=SimpleNamespace(name="list_data_sources", arguments="{}"),
        )
        responses = [
            SimpleNamespace(
                choices=[
                    SimpleNamespace(
                        message=FakeMessage(tool_calls=[call]), finish_reason="tool_calls"
                    )
                ]
            ),
            SimpleNamespace(
                choices=[
                    SimpleNamespace(
                        message=FakeMessage(content="The orders source is available."),
                        finish_reason="stop",
                    )
                ]
            ),
        ]

        class FakeCompletions:
            def __init__(self):
                self.requests = []

            async def create(self, **kwargs):
                self.requests.append(kwargs)
                return responses.pop(0)

        completions = FakeCompletions()
        client = SimpleNamespace(chat=SimpleNamespace(completions=completions))
        session = FakeSession([result({
            "data_sources": [{"id": "ds-1", "name": "orders", "type": "postgres", "status": "active"}],
            "count": 1,
        })])
        bridge = DataFabricFunctions(session)

        answer = await run_agent(client, "deepseek-flash", bridge, "What data sources can I query?")

        self.assertEqual(answer, "The orders source is available.")
        self.assertEqual(len(completions.requests), 2)
        self.assertTrue(any(message["role"] == "tool" for message in completions.requests[1]["messages"]))

    async def test_final_answer_marks_unfetched_rows_as_partial(self):
        call = SimpleNamespace(
            id="call-1",
            function=SimpleNamespace(
                name="list_tables",
                arguments='{"datasource_id":"ds-1"}',
            ),
        )
        responses = [
            SimpleNamespace(
                choices=[
                    SimpleNamespace(
                        message=FakeMessage(tool_calls=[call]), finish_reason="tool_calls"
                    )
                ]
            ),
            SimpleNamespace(
                choices=[
                    SimpleNamespace(
                        message=FakeMessage(content="The first table is orders."),
                        finish_reason="stop",
                    )
                ]
            ),
        ]

        class FakeCompletions:
            async def create(self, **kwargs):
                return responses.pop(0)

        client = SimpleNamespace(
            chat=SimpleNamespace(completions=FakeCompletions())
        )
        session = FakeSession([
            result({
                "queryId": "internal-query",
                "slug": "internal-slug",
                "token": "internal-token",
                "nextUri": "/fetch/next",
                "columns": [{"name": "table_name", "type": "string"}],
                "entries": [["orders"]],
                "totalCount": 2,
            })
        ])
        bridge = DataFabricFunctions(session)

        answer = await run_agent(client, "deepseek-flash", bridge, "List tables.")

        self.assertIn("may be partial", answer)
        self.assertNotIn("internal-token", answer)

    async def test_fetching_final_page_clears_partial_warning(self):
        list_call = SimpleNamespace(
            id="call-1",
            function=SimpleNamespace(
                name="list_tables",
                arguments='{"datasource_id":"ds-1"}',
            ),
        )
        next_call = SimpleNamespace(
            id="call-2",
            function=SimpleNamespace(
                name="fetch_next_batch",
                arguments='{"result_id":"result_1"}',
            ),
        )
        responses = [
            SimpleNamespace(
                choices=[
                    SimpleNamespace(
                        message=FakeMessage(tool_calls=[list_call]), finish_reason="tool_calls"
                    )
                ]
            ),
            SimpleNamespace(
                choices=[
                    SimpleNamespace(
                        message=FakeMessage(tool_calls=[next_call]), finish_reason="tool_calls"
                    )
                ]
            ),
            SimpleNamespace(
                choices=[
                    SimpleNamespace(
                        message=FakeMessage(content="Both tables are orders and items."),
                        finish_reason="stop",
                    )
                ]
            ),
        ]

        class FakeCompletions:
            async def create(self, **kwargs):
                return responses.pop(0)

        client = SimpleNamespace(
            chat=SimpleNamespace(completions=FakeCompletions())
        )
        session = FakeSession([
            result({
                "queryId": "internal-query",
                "slug": "internal-slug",
                "token": "internal-token",
                "nextUri": "/fetch/next",
                "columns": [{"name": "table_name", "type": "string"}],
                "entries": [["orders"]],
                "totalCount": 2,
            }),
            result({
                "queryId": "internal-query",
                "slug": "internal-slug",
                "token": "rotated-token",
                "nextUri": "",
                "columns": [{"name": "table_name", "type": "string"}],
                "entries": [["items"]],
                "totalCount": 2,
            }),
        ])
        bridge = DataFabricFunctions(session)

        answer = await run_agent(client, "deepseek-flash", bridge, "List tables.")

        self.assertEqual(answer, "Both tables are orders and items.")
        self.assertNotIn("internal-token", answer)
        self.assertNotIn("result_1", bridge.cursors)


if __name__ == "__main__":
    unittest.main()
