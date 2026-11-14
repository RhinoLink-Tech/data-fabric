"""Build an English storyboard from the original Chinese explainer.

This only localizes text and narration. Generated-image prompts are retained,
and the shared image files must be attached separately before publishing.
"""
from pathlib import Path
from html import escape, unescape
import re
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent
SOURCE = ROOT / "explainer.zh.xml"
OUTPUT = ROOT / "explainer.en.xml"

# The keys are the original stable anchors, so layouts, ordering and pointers
# stay paired with the Chinese storyboard.
labels = {
    "intro": ("Data Fabric: From Data to AI Apps", "Connect authorized data sources to AI applications through a self-hosted gateway."),
    "connected": ("Connect scattered data", None),
    "business": ("One business scenario", None),
    "orders": ("Orders", "Transaction records in the business database"),
    "analytics": ("Analytics", "Business summaries in an analytics system"),
    "files": ("Files", "Structured records in object storage"),
    "assistants": ("Applications", "Business services, AI assistants and analytics"),
    "architecture": ("Applications, gateway and sources", None),
    "deployment": ("Deploy in your own environment", None),
    "onboard": ("From startup to a first query", None),
    "onboard-config": ("Prepare configuration", "Service address, keys, metadata and identity provider"),
    "onboard-start": ("Start the services", "Published image and Compose deployment"),
    "onboard-check": ("Check status", "Health checks and deployment diagnostics"),
    "onboard-source": ("Register a source", "Connection settings, a read-only account and grants"),
    "management": ("Manage sources once", None),
    "mg-life": ("Lifecycle", "Create, inspect, update, activate and deactivate"),
    "mg-check": ("Connection diagnostics", "Validate settings, test connectivity and check health"),
    "mg-use": ("Shared entry point", "Management UI and REST API"),
    "sources": ("Connect different data types", None),
    "src-db": ("Relational databases", "MySQL, PostgreSQL, SQL Server and others"),
    "src-olap": ("Analytics and warehouses", "ClickHouse, Doris, StarRocks and others"),
    "src-object": ("Objects and files", "S3 or MinIO; CSV, JSON, Parquet and more"),
    "src-lake": ("Lakehouse and query engines", "Supported table formats and engines such as Trino"),
    "schema": ("Understand data before querying", None),
    "schema-find": ("Discover sources", "Find connections available to the caller"),
    "schema-tables": ("Discover tables", "List the tables available to query"),
    "schema-cols": ("Inspect columns", "Column names, types and table structure"),
    "schema-search": ("Search schema", "Find data relevant to the task"),
    "query": ("Query from SQL to results", "Synthetic orders example"),
    "dialect": ("SQL dialect adaptation", None),
    "dialect-in": ("Input dialect", "Declare it with sourceDialect"),
    "dialect-map": ("Translate syntax", "Convert supported query constructs"),
    "dialect-out": ("Execute at the target", "Send the query to the selected source"),
    "pagination": ("Fetch batches until results are complete", None),
    "control": ("Keep query consumption manageable", None),
    "ctl-bound": ("Parameter binding", "Separate supported inputs from the query structure"),
    "ctl-time": ("Timeout and cancellation", "Bound individual requests"),
    "ctl-budget": ("Resource budgets", "Configure concurrency, rates and result size"),
    "federation": ("Add cross-source queries when needed", None),
    "fed-register": ("Register a federation engine", "Configure an engine such as Trino as a source"),
    "fed-plan": ("Query across catalogs", "The engine connects the relevant systems"),
    "fed-result": ("Return results", "Applications continue to use the gateway"),
    "security-hero": ("Share data with identities and permissions", None),
    "policy": ("Control access to tables and columns", None),
    "pol-table": ("Table rules", "Allow access to selected tables"),
    "pol-col": ("Column rules", "Restrict fields that should not be exposed"),
    "pol-read": ("Read-only checks", "Validate queries and use restricted source accounts"),
    "pol-audit": ("Redacted audit", "Record access decisions and query events"),
    "policy-life": ("Manage policy changes", None),
    "policy-check": ("Review", "Check a proposed policy before publication"),
    "policy-publish": ("Publish", "Apply the intended policy version"),
    "policy-history": ("Review and adjust", "Use supported history and rollback workflows"),
    "mcp-skill": ("MCP tools and Skill workflows", None),
    "mcp-tools": ("MCP tools", "Discover, inspect, validate, query and fetch"),
    "skill-flow": ("Agent Skill", "Guide the assistant through the workflow"),
    "client-identity": ("Caller identity", "Use the configured authentication and grants"),
    "codex": ("Codex: connect data to development", None),
    "codex-skill": ("Install the Skill", "Keep the complete Skill and its references"),
    "codex-mcp": ("Configure MCP", "Set the service endpoint and authentication"),
    "codex-task": ("Start a task", "Select a source, inspect schema and query"),
    "workbuddy": ("WorkBuddy: ask a data question", None),
    "wb-import": ("Import the Skill", "Upload the Skill package"),
    "wb-connect": ("Configure a connector", "Set the MCP endpoint and authentication"),
    "wb-ask": ("Ask a question", "Specify the source, result and batch size"),
    "reuse": ("One data gateway, many applications", None),
    "apps": ("Connect AI workflows to data", None),
    "app-agent": ("AI agents", "Retrieve authorized business context"),
    "app-rag": ("RAG data access", "Retrieve structured and semi-structured records"),
    "app-t2s": ("Text to SQL", "Validate and execute supported model-generated queries"),
    "app-api": ("REST and custom apps", "Connect existing services and tool workflows"),
    "operations": ("Observe service and query activity", None),
    "ops-health": ("Services and connections", "Health, connectivity and connection pools"),
    "ops-query": ("Query lifecycle", "Elapsed time, rows, batches and completion"),
    "ops-metrics": ("Metrics and audit", "Authorized metrics and redacted events"),
    "footprint": ("Size resources for your workload", None),
    "foot-conn": ("Connections on demand", "Open connections for configured sources"),
    "foot-path": ("Choose a query path", "Use a direct driver or a federation engine"),
    "foot-budget": ("Control consumption", "Tune batches, concurrency and result budgets"),
    "recovery": ("Protect configuration and plan recovery", None),
    "rec-encrypt": ("Protect credentials", "Use the configured keyring and encryption"),
    "rec-backup": ("Encrypted backup", "Check the supported metadata deployment profile"),
    "rec-restore": ("Verify and restore", "Check integrity and rehearse an isolated restore"),
    "extension": ("Extend around shared interfaces", None),
    "ext-driver": ("Driver interfaces", "Add or select source integrations"),
    "ext-catalog": ("Capability catalog", "Check source types and compatibility evidence"),
    "ext-workflow": ("Application integration", "Build workflows with REST, MCP and Skills"),
    "start": ("Put data to work in applications", None),
    "start-deploy": ("Deploy the gateway", "Prepare the service and identity settings"),
    "start-connect": ("Connect data", "Register a source and configure grants"),
    "start-app": ("Connect an application", "Use REST, Codex, WorkBuddy or your own agent"),
    "start-query": ("Complete a task", "Discover, validate, query and fetch results"),
}

speeches = [
    ("Data Fabric", "[Data Fabric](#intro) connects authorized enterprise data to applications through one gateway."),
    ("From data to applications", "[Enterprise data](#connected) often lives in business databases, analytics systems and object storage. Each application needs connections, authentication, query handling and result formats. The gateway centralizes those shared steps while data stays in the original systems."),
    ("An order-data scenario", "[Order analysis](#business) connects the examples in this overview. [Orders](#orders) record transactions, [analytics](#analytics) supplies business summaries, and [files](#files) contain structured records. Authorized [applications and assistants](#assistants) find data, inspect its structure and retrieve results within their permissions."),
    ("A shared access path", "[A request](#architecture) travels from an application through the gateway, where configured identity, permissions and routing are applied, to the target source. Results return through the same gateway. Teams can maintain one connection while authorized applications use separate identities."),
    ("Self-hosted deployment", "[Self-hosted deployment](#deployment) runs the gateway in your own environment. A published image avoids local compilation. Prepare the required configuration, metadata storage and authentication, then start the services and use the health checks. Follow the guide for the exact release and profile."),
    ("From startup to a first query", "[A first deployment](#onboard) has four steps. [Prepare configuration](#onboard-config), [start the services](#onboard-start), [check health and diagnostics](#onboard-check), then [register a source](#onboard-source) with a restricted account and suitable caller grants."),
    ("Source management", "[Source management](#management) centralizes connection details. The [lifecycle](#mg-life) covers setup and changes; [diagnostics](#mg-check) help identify connectivity problems; and the [management UI and API](#mg-use) serve operators and automation. Authorized applications can reuse the registered source."),
    ("Data-source options", "[Source integrations](#sources) cover several data shapes: [relational databases](#src-db), [analytics systems](#src-olap), [objects and files](#src-object), and [lakehouse or query engines](#src-lake). Check the support catalog for the exact source, format, version and verification status before relying on a connector."),
    ("Schema discovery", "[Schema discovery](#schema) gives an application the context to form a query. [Find an authorized source](#schema-find), [inspect its tables](#schema-tables), [check column names and types](#schema-cols), and [search the schema](#schema-search) for the relevant business data."),
    ("A query example", "[This synthetic query](#query) [selects the identifier](#query:L1 'SELECT id') from [the sample orders table](#query:L2 'sample.orders') and [sorts it in ascending order](#query:L3 'ORDER BY id ASC'). Both business programs and authorized assistants can use the query workflow."),
    ("Dialect adaptation", "[Dialect adaptation](#dialect) can address supported syntax differences. [Declare the input dialect](#dialect-in), apply [supported translations](#dialect-map), and [execute at the target](#dialect-out). Native target syntax is another option. Consult the published compatibility matrix for the actual scope."),
    ("Batched results", "[Batch retrieval](#pagination) lets a client receive a result in parts. In this synthetic example the first batch contains one and two, the next contains three, and the final state marks the result complete. Streaming behavior depends on the selected driver."),
    ("Query controls", "[Query controls](#control) make shared execution manageable. [Parameter binding](#ctl-bound) passes supported values, [timeouts and cancellation](#ctl-time) bound requests, and [resource budgets](#ctl-budget) configure limits such as concurrency, rate and result size."),
    ("Cross-source access", "[Cross-source access](#federation) is optional. [Register a federation engine](#fed-register), let that engine [plan the cross-catalog query](#fed-plan), and [return results](#fed-result) through the gateway. Direct single-source requests use the corresponding driver."),
    ("Controlled sharing", "[Controlled sharing](#security-hero) begins with the caller identity. The gateway applies configured owner, tenant and source permissions; a restricted database account adds a source-side boundary. Remote MCP requires the authentication and transport described in the release guide."),
    ("Table and column policies", "[Access policies](#policy) can use [table rules](#pol-table) and [column rules](#pol-col) where supported. [Read-only validation](#pol-read) works alongside restricted source accounts, and [redacted audit events](#pol-audit) record access and query activity. Verify both allowed and denied paths in your environment."),
    ("Policy changes", "[Policy changes](#policy-life) can follow an operational workflow: [review a proposed change](#policy-check), [publish a version](#policy-publish), then use supported [history and rollback](#policy-history) when needed. Consult the documentation for the exact operations and scope."),
    ("MCP and Skills", "[Assistant integration](#mcp-skill) combines [MCP tools](#mcp-tools) for discovery, schema inspection, validation, queries and batches, with [Skill instructions](#skill-flow) that guide their use. The [caller identity](#client-identity) determines the configured access."),
    ("Codex", "[Codex setup](#codex) starts by [installing the complete Skill](#codex-skill) and its references. [Configure the MCP connection](#codex-mcp), then [start a data task](#codex-task) that discovers the source, inspects the schema and retrieves authorized results."),
    ("WorkBuddy", "[WorkBuddy setup](#workbuddy) uses a client-specific workflow. [Import the Skill](#wb-import), [configure an MCP connector](#wb-connect), and [ask a business question](#wb-ask). Marketplace availability and direct HTTPS client acceptance require separate verification; consult the current integration guide."),
    ("Reuse across applications", "[A shared source](#reuse) can serve a developer assistant, a desktop assistant and a business service. Each application uses its own configured identity, while the team maintains the connection in one place. Access remains subject to the grants that were actually configured and tested."),
    ("Application scenarios", "[Applications](#apps) use the gateway in different workflows. [AI agents](#app-agent) obtain business context, [retrieval pipelines](#app-rag) read records for downstream processing, [text-to-SQL](#app-t2s) submits supported queries, and [business services](#app-api) use the API. Model inference and orchestration stay in the application stack."),
    ("Observability", "[Operational visibility](#operations) includes [service and connection health](#ops-health), the [query lifecycle](#ops-query), and authorized [metrics and redacted audit events](#ops-metrics). Teams can distinguish a partially fetched result from a completed retrieval and review requests using the supported instrumentation."),
    ("Resource choices", "[Resource use](#footprint) depends on the actual workload. [Connections](#foot-conn) are opened for configured sources, [routing](#foot-path) is selected for the task, and [batch and execution budgets](#foot-budget) can be tuned. The standard build includes registered drivers; measure startup time and memory on your own deployment."),
    ("Backup and recovery", "[Recovery planning](#recovery) includes protecting [credentials and keys](#rec-encrypt), checking the release-specific [backup procedure](#rec-backup), and rehearsing an [isolated restore](#rec-restore). Backup support and limitations vary by release and profile; do not assume an untested path is supported."),
    ("Extension", "[The gateway](#extension) can grow through [driver interfaces](#ext-driver), a [capability catalog](#ext-catalog), and [application interfaces](#ext-workflow). Start with one verified data source and task, then check compatibility and permissions for additional integrations."),
    ("Get started", "[Getting started](#start) means [deploying the gateway](#start-deploy), [registering and authorizing a source](#start-connect), [connecting an application](#start-app), and [completing a representative query](#start-query). Use the versioned guides and known limitations. Feedback, contributions and a GitHub star are welcome."),
]

source = SOURCE.read_text(encoding="utf-8")
original = ET.fromstring(source)
original_items = list(original.findall("./item"))
assert len(original_items) == len(speeches) == 27

seen = set()
def replace_item(match):
    tag = match.group(0)
    found = re.search(r'\banchor="([^"]+)"', tag)
    if not found:
        return tag
    anchor = found.group(1)
    if anchor not in labels:
        raise ValueError(f"Missing English label for {anchor}")
    seen.add(anchor)
    title, text = labels[anchor]
    tag = re.sub(r'\btitle="[^"]*"', f'title="{escape(title, quote=True)}"', tag, count=1)
    if text is not None:
        if not re.search(r'\btext="[^"]*"', tag):
            raise ValueError(f"Expected a text attribute for {anchor}")
        tag = re.sub(r'\btext="[^"]*"', f'text="{escape(text, quote=True)}"', tag, count=1)
    return tag

english = re.sub(r'<item\b[^>]*>', replace_item, source)
assert seen == set(labels), f"Unused English labels: {set(labels) - seen}"

say_index = 0
def replace_say(match):
    global say_index
    title, speech = speeches[say_index]
    say_index += 1
    return f'<say lang="en-US" title="{escape(title, quote=True)}" text="{escape(speech, quote=True)}"/>'

english = re.sub(r'<say\b[^>]*/>', replace_say, english)
assert say_index == len(speeches)

# Keep the original animation geometry, timing, colors and transitions while
# localizing only visible labels inside the two animation source blocks.
animation_text = {
    "业务应用 / AI": "Applications / AI",
    "原始数据源": "Source systems",
    "连接管理 · 身份与权限 · 查询路由": "Connections · Identity · Query routing",
    "首批：1，2": "First batch: 1, 2",
    "续批：3": "Next batch: 3",
    "结果完整": "Results complete",
    "取回所有批次，明确结果是否完整": "Fetch all batches and confirm completion",
}
for old, new in animation_text.items():
    assert old in english
    english = english.replace(old, new)

followups = iter([
    "How do I deploy Data Fabric and register a read-only source?",
    "How do I connect Data Fabric MCP and Skills to an assistant?",
])
english = re.sub(r'<followup\b[^>]*/>', lambda _: f'<followup prompt="{escape(next(followups), quote=True)}"/>', english)

localized = ET.fromstring(english)
new_items = localized.findall("./item")
assert [x.attrib.get("anchor") for x in new_items] == [x.attrib.get("anchor") for x in original_items]
assert [(x.attrib.get("type"), x.attrib.get("layout")) for x in new_items] == [(x.attrib.get("type"), x.attrib.get("layout")) for x in original_items]
assert all(x.attrib.get("lang") == "en-US" for x in localized.findall("./say"))
assert not re.search(r'[\u3400-\u9fff]', english), "Untranslated Chinese remains in the draft"
for before, after in zip(original_items, new_items):
    if before.attrib.get("type") == "image":
        assert before.attrib["prompt"] == after.attrib["prompt"], "Image prompts changed"
    assert [x.attrib.get("anchor") for x in before.findall("./item")] == [x.attrib.get("anchor") for x in after.findall("./item")]

OUTPUT.write_text(english, encoding="utf-8")
transcript = ROOT / "transcript.en.md"
transcript.write_text("# Data Fabric product overview — English draft\n\n" + "\n\n".join(f"## {title}\n\n{re.sub(r'\[([^]]+)\]\(#[^)]*\)', r'\1', text)}" for title, text in speeches) + "\n", encoding="utf-8")
print(f"Built {OUTPUT.name}; {len(new_items)} scenes; {say_index} English narration blocks; 4 original image prompts retained.")
print("Upload the same shared image files to each video before publishing.")
