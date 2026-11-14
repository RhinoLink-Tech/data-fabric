# Data Fabric product overview — English draft

## Data Fabric

Data Fabric connects authorized enterprise data to applications through one gateway.

## From data to applications

Enterprise data often lives in business databases, analytics systems and object storage. Each application needs connections, authentication, query handling and result formats. The gateway centralizes those shared steps while data stays in the original systems.

## An order-data scenario

Order analysis connects the examples in this overview. Orders record transactions, analytics supplies business summaries, and files contain structured records. Authorized applications and assistants find data, inspect its structure and retrieve results within their permissions.

## A shared access path

A request travels from an application through the gateway, where configured identity, permissions and routing are applied, to the target source. Results return through the same gateway. Teams can maintain one connection while authorized applications use separate identities.

## Self-hosted deployment

Self-hosted deployment runs the gateway in your own environment. A published image avoids local compilation. Prepare the required configuration, metadata storage and authentication, then start the services and use the health checks. Follow the guide for the exact release and profile.

## From startup to a first query

A first deployment has four steps. Prepare configuration, start the services, check health and diagnostics, then register a source with a restricted account and suitable caller grants.

## Source management

Source management centralizes connection details. The lifecycle covers setup and changes; diagnostics help identify connectivity problems; and the management UI and API serve operators and automation. Authorized applications can reuse the registered source.

## Data-source options

Source integrations cover several data shapes: relational databases, analytics systems, objects and files, and lakehouse or query engines. Check the support catalog for the exact source, format, version and verification status before relying on a connector.

## Schema discovery

Schema discovery gives an application the context to form a query. Find an authorized source, inspect its tables, check column names and types, and search the schema for the relevant business data.

## A query example

This synthetic query selects the identifier from the sample orders table and sorts it in ascending order. Both business programs and authorized assistants can use the query workflow.

## Dialect adaptation

Dialect adaptation can address supported syntax differences. Declare the input dialect, apply supported translations, and execute at the target. Native target syntax is another option. Consult the published compatibility matrix for the actual scope.

## Batched results

Batch retrieval lets a client receive a result in parts. In this synthetic example the first batch contains one and two, the next contains three, and the final state marks the result complete. Streaming behavior depends on the selected driver.

## Query controls

Query controls make shared execution manageable. Parameter binding passes supported values, timeouts and cancellation bound requests, and resource budgets configure limits such as concurrency, rate and result size.

## Cross-source access

Cross-source access is optional. Register a federation engine, let that engine plan the cross-catalog query, and return results through the gateway. Direct single-source requests use the corresponding driver.

## Controlled sharing

Controlled sharing begins with the caller identity. The gateway applies configured owner, tenant and source permissions; a restricted database account adds a source-side boundary. Remote MCP requires the authentication and transport described in the release guide.

## Table and column policies

Access policies can use table rules and column rules where supported. Read-only validation works alongside restricted source accounts, and redacted audit events record access and query activity. Verify both allowed and denied paths in your environment.

## Policy changes

Policy changes can follow an operational workflow: review a proposed change, publish a version, then use supported history and rollback when needed. Consult the documentation for the exact operations and scope.

## MCP and Skills

Assistant integration combines MCP tools for discovery, schema inspection, validation, queries and batches, with Skill instructions that guide their use. The caller identity determines the configured access.

## Codex

Codex setup starts by installing the complete Skill and its references. Configure the MCP connection, then start a data task that discovers the source, inspects the schema and retrieves authorized results.

## WorkBuddy

WorkBuddy setup uses a client-specific workflow. Import the Skill, configure an MCP connector, and ask a business question. Marketplace availability and direct HTTPS client acceptance require separate verification; consult the current integration guide.

## Reuse across applications

A shared source can serve a developer assistant, a desktop assistant and a business service. Each application uses its own configured identity, while the team maintains the connection in one place. Access remains subject to the grants that were actually configured and tested.

## Application scenarios

Applications use the gateway in different workflows. AI agents obtain business context, retrieval pipelines read records for downstream processing, text-to-SQL submits supported queries, and business services use the API. Model inference and orchestration stay in the application stack.

## Observability

Operational visibility includes service and connection health, the query lifecycle, and authorized metrics and redacted audit events. Teams can distinguish a partially fetched result from a completed retrieval and review requests using the supported instrumentation.

## Resource choices

Resource use depends on the actual workload. Connections are opened for configured sources, routing is selected for the task, and batch and execution budgets can be tuned. The standard build includes registered drivers; measure startup time and memory on your own deployment.

## Backup and recovery

Recovery planning includes protecting credentials and keys, checking the release-specific backup procedure, and rehearsing an isolated restore. Backup support and limitations vary by release and profile; do not assume an untested path is supported.

## Extension

The gateway can grow through driver interfaces, a capability catalog, and application interfaces. Start with one verified data source and task, then check compatibility and permissions for additional integrations.

## Get started

Getting started means deploying the gateway, registering and authorizing a source, connecting an application, and completing a representative query. Use the versioned guides and known limitations. Feedback, contributions and a GitHub star are welcome.
