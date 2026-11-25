# WorkBuddy self-service onboarding (opt-in)

This experimental path provisions an isolated synthetic workspace on the first
valid OIDC access token with a verified email. It is disabled by default. It
is intended only for a dedicated WorkBuddy environment, not for provisioning
access to an existing organization's data.

## Prerequisites

- Configure the existing WorkBuddy onboarding runtime, OIDC validation, audit
  key, credential vault, and restrictive egress policy.
- An operator must create an active PostgreSQL template source for the synthetic
  `synthetic_orders` database using the SELECT-only `demo_reader` account.
  Set `DATA_FABRIC_WORKBUDDY_SELF_SERVICE_TEMPLATE_ID` to its UUID.
- Set `DATA_FABRIC_WORKBUDDY_SELF_SERVICE=true` only for that isolated
  deployment. The server rejects this configuration without WorkBuddy OIDC
  onboarding or a valid template identifier.
- Configure account registration, verified email, mail delivery and password
  recovery in the identity provider. Before enabling verification, account
  for existing unverified users: Keycloak may require them to verify their
  existing address on login. Do not mark an address verified without proof.

A signed token must contain the verified email claim and no preexisting tenant
for automatic provisioning. The tenant is derived from issuer and subject;
the grant, source copy and audit record are transactional. A revoked grant is
not automatically restored. Existing tenant-bearing tokens follow the normal
path. Source credentials are re-encrypted by the vault; they are never
included in the MCP tool response.

An operator can seed the synthetic source for an individually provisioned
subject with the `workbuddy-seed-synthetic` server command. Run it within the
same isolated deployment with `--subject <OIDC UUID> --tenant t-<OIDC UUID> --template <source UUID>`. This does not create
an identity or grant and is restricted to the validated synthetic template.

For a narrow reverse-proxy configuration with registration, account switching and logout, see [workbuddy-identity-proxy.md](workbuddy-identity-proxy.md).

## Shared test sources

An operator may opt in to a shared test catalogue by setting
`DATA_FABRIC_WORKBUDDY_SHARED_TEST_CATALOG` to the path of a JSON file mounted
read-only in the gateway container. The file must be readable by the gateway
user and have permissions no broader than `0600`; never commit it or include it
in a Skill or connector package. It is loaded at startup. Each array entry has a
stable `key`, display `name`, `type` (`mysql` or `postgresql`), `readOnly: true`,
and a `config` containing the connection fields. For example, replace all
placeholders locally:

```json
[{"key":"orders","name":"Orders","type":"mysql","readOnly":true,
  "config":{"host":"10.0.0.10","port":3306,"database":"test_orders",
    "username":"orders_reader","password":"<locally-provisioned-secret>",
    "ssl":false,"timeout":5,"maxPoolSize":2}}]
```

The `readOnly` field is an operator assertion: **it does not create a database
privilege**. Independently grant each account only the intended SELECT
permissions, verify the dataset is appropriate for all users of the deployment,
and restrict the fixed-IP egress policy to approved endpoints. Never put real
user data or administrative database credentials in this catalogue.

After a verified self-service user authenticates, the server creates a separate,
credential-encrypted metadata record for each shared test source in that user's
tenant. Existing accounts are reconciled on their next authenticated request;
private sources continue to use their existing ownership rules. Removing an
entry or using an empty array disables its managed metadata record on the next
request from each affected account. Credential changes are applied at that
point as well. Restart the gateway to load catalogue changes; to revoke access
immediately, also revoke the underlying database account and terminate any
active database sessions as appropriate. Removing the environment setting
also disables previously managed entries on their next authenticated request.
Tenant-bearing tokens that bypass self-service provisioning do not receive these
entries automatically.

## Verification

Run the focused Go tests. The optional integration test requires Docker,
Go, OpenSSL, Python and the `requests` package:

```sh
python3 scripts/test-workbuddy-self-service-identity.py
```

It creates disposable local services and a fake SMTP server, runs registration,
email verification, password recovery, data source management, two-user MCP
isolation and a synthetic PostgreSQL query,
and deletes its own containers and network. It does not send real email.

Before production rollout, separately verify real mail delivery, password
recovery, the platform's OAuth and refresh flow, and management operations for a
new user. A local integration test does not establish those properties.
