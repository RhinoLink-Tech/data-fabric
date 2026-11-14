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

## Verification

Run the focused Go tests. The optional integration test requires Docker,
Go, OpenSSL, Python and the `requests` package:

```sh
python3 scripts/test-workbuddy-self-service-identity.py
```

It creates disposable local services and a fake SMTP server, runs registration,
email verification, password recovery, two-user MCP isolation and a synthetic PostgreSQL query,
and deletes its own containers and network. It does not send real email.

Before production rollout, separately verify real mail delivery, password
recovery, the platform's OAuth and refresh flow, and management operations for a
new user. A local integration test does not establish those properties.
