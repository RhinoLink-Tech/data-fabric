# Expanding browser-based source onboarding

[中文](workbuddy-source-onboarding-expansion-zh.md)

Status: planning, not a supported feature. The opt-in browser workflow is
limited by default to MySQL and PostgreSQL. MariaDB, openGauss and TiDB are separate,
default-off source candidates and require explicit administrator opt-in. Source-type
registration, the management UI, and a driver contract do not by themselves
make a type eligible for the browser workflow. See the [WorkBuddy guide](../integrations/workbuddy/README.md)
and [connector version policy](connector-version-policy.md).

## Product task and evidence

The candidate task is an operator connecting an isolated, synthetic order
system and asking an AI client to discover its schema and count orders. The
operator enters database credentials in the authenticated browser, never in the
model conversation. This is a reproducible engineering scenario, not evidence
of customer demand or a completed WorkBuddy client acceptance. User interviews,
actual source inventory, production network constraints, and priority by real
usage are still needed before making a broad adoption claim.

A source is a candidate only when its actual driver, authentication, restricted
network path, TLS, read-only behavior, metadata and result contracts have
adequate evidence for a specific version. The public type inventory must
continue to distinguish a registered connector from the browser's currently
allowed type gate, and from whether an individual user is authorized.

## Provisional order

| Candidate | Existing evidence | Decision and remaining work |
| --- | --- | --- |
| MariaDB | The catalog records real driver and TLS contracts for specific releases, including 10.11.19, 11.4.13, 11.8.9 and 12.3.3; it uses the MySQL protocol. | The default-off source candidate ([#761](https://github.com/RhinoLink-Tech/data-fabric/issues/761)) exercises this type separately through the restricted browser/probe/query path. A synthetic protocol-level full lifecycle was exercised; actual WorkBuddy client and published-image acceptance remain open. MySQL compatibility alone is not proof. |
| openGauss | The catalog records SQL, metadata, database-side read-only and TLS for 6.0.0 build aee4abd5. | A separate default-off, TLS-only candidate is tracked in [#781](https://github.com/RhinoLink-Tech/data-fabric/issues/781); real-client acceptance and a supported release remain open. It is not included in the experimental Core build. |
| TiDB | The catalog records driver and TLS contracts for 8.5.8. Its contract does not establish a distributed TiKV topology. | A separate default-off, TLS-only candidate is tracked in [#834](https://github.com/RhinoLink-Tech/data-fabric/issues/834). The gateway has no verified TiDB read-only session initializer: an administrator must verify a SELECT-only source account. Browser/client and release gates remain separate. |
| ClickHouse | The catalog records a native-protocol and TLS contract for 26.8.20.9; an isolated SELECT-only account passed authorized reads and denied writes/other-table access ([#841](https://github.com/RhinoLink-Tech/data-fabric/issues/841)). | Separate design: the restricted connector is currently MySQL/PostgreSQL-specific; native-protocol security and diagnostics need their own path. |
| SaaS and file/object sources | Registration or a service API alone does not establish a secure interactive flow. | Separate design for OAuth, scopes, file/path controls, pagination and rate limits; see #755 for the business-source evaluation. |

These are exact contract observations, not supported version ranges or
promises that the onboarding flow works. Recheck the generated
[connector catalog](data-source-support.md) when implementing: evidence can
change independently of this document.

## Implementation gates for the first candidate

1. Record a target version, synthetic business scenario and source-specific
   fields. Use an explicitly configured source type, not a silent MySQL alias.
2. Extend the explicit type gate in `internal/workbuddy/onboardinghttp/handler.go`
   and the allowed list in `internal/workbuddy/onboardingruntime/runtime.go`
   only with a matching restricted implementation. Keep the workflow opt-in.
3. Review `AllowedProbe.Pin` and `FixedIPEgressPolicy` as well as the connector:
   restrict host/port to an operator allowlist, pin resolved IPs, retain the
   original hostname for certificate verification, prevent redirects or
   fallback to an unrestricted connection, and bound probe/query resources.
   Require a source-side read-only account and verify its effective privileges.
4. Check password intake, encryption at rest, redacted diagnostics and audit,
   session binding, CSRF, expiration, authorization, retries, and update/delete
   behavior. Do not place credentials in MCP tool arguments or logs.
5. In an isolated deployment run create → test → schema → read-only query →
   bounded continuation → update → deactivate → delete. Include untrusted CA,
   hostname mismatch, denied address/port, wrong credentials, unauthorized user,
   cross-tenant access and failed probe; verify that failures do not disclose
   credentials or partially authorize a source. Record image digests, server
   versions, code SHA, operator and observed output.
6. Verify actual WorkBuddy client behavior separately from protocol-level tests.
   Until that succeeds, label the feature as a source candidate and do not
   advertise a complete self-service onboarding journey in the marketplace.

No local image download is required to study this plan. Isolated remote CI can
run the pinned database contracts. A developer running a local fixture should
explicitly opt into any image download or use already cached images.

See also the [Feishu Bitable read-only candidate evaluation](feishu-bitable-evaluation.md).
