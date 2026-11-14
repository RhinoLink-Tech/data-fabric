# WorkBuddy identity proxy example

`deploy/mcp/workbuddy-identity-proxy.conf.template` captures the narrow proxy routes used by an isolated WorkBuddy environment. It provides registration, account, account switching and logout alongside the existing MCP and browser onboarding routes. It is an example for operators who already have the isolated onboarding stack; it is not a general-purpose Keycloak reverse proxy.

The example assumes:

- Keycloak realm `data-fabric`, served under `/identity`, and upstreams `identity:8080` and `gateway:8082` on a private Docker network.
- HTTPS is terminated at a trusted tunnel or ingress, with an internal certificate available at `/etc/df-proxy/tls.crt` and `/etc/df-proxy/tls.key`.
- The Keycloak `account-console` is enabled; registration requires verified email, a configured mail provider and password recovery.
- A dedicated public client `data-fabric-account-signup` uses authorization code with S256 PKCE. Its only login redirect is `https://<public-host>/signup/complete`; valid post-logout redirects are exactly `https://<public-host>/register/start` and `https://<public-host>/account/start`.
- The gateway already has an authorized synthetic source template and the opt-in self-service provisioning described in [workbuddy-self-service.md](workbuddy-self-service.md).

Substitute only the URL-safe values `DF_PUBLIC_HOST`, `DF_SIGNUP_CHALLENGE` and `DF_SIGNUP_STATE` when rendering. Restrict Docker's Nginx envsubst filter to those names so Nginx variables are preserved. Validate the hostname and the base64url challenge and state before rendering.

For example, with Bash and Python available, generate URL-safe values without retaining the verifier:

```sh
export DF_PUBLIC_HOST=your-public-host.example
read -r DF_SIGNUP_CHALLENGE DF_SIGNUP_STATE < <(python3 -c 'import base64,hashlib,secrets; v=secrets.token_urlsafe(64); print(base64.urlsafe_b64encode(hashlib.sha256(v.encode()).digest()).rstrip(b"=").decode(),secrets.token_urlsafe(24))')
export DF_SIGNUP_CHALLENGE DF_SIGNUP_STATE
export NGINX_ENVSUBST_FILTER='^(DF_PUBLIC_HOST|DF_SIGNUP_CHALLENGE|DF_SIGNUP_STATE)$'
```

The signup-only client completes registration and ends at a static page; it does not obtain access tokens or authorize a WorkBuddy connector. Generate the PKCE challenge from a strong random verifier and discard the verifier: this flow does not redeem its authorization code. WorkBuddy must separately initiate its own OAuth authorization. Neither the challenge nor state is a credential, but do not put real access tokens, passwords or client secrets into a template.

The switch and registration start routes explicitly expire stale Keycloak authentication-flow cookies at the realm path after logout. Their redirects use the configured public HTTPS origin, never the internal proxy port. The account console is Keycloak's authenticated self-service interface, not an admin console; a JSON request without a token must be denied. The proxy denies all unlisted paths.

To check the template contract and the disposable Keycloak, SMTP, PostgreSQL, MCP and restricted-proxy flows:

```sh
go test ./cmd/server -run TestWorkBuddyIdentityProxyContract
python3 scripts/test-workbuddy-self-service-identity.py
```

The integration test uses only generated identities, fake SMTP and disposable local containers. It verifies logout, account visibility, stale-cookie cleanup, HTTPS redirects, registration after an old session, source management and cross-account isolation. Independently verify the rendered configuration and native clients in each deployment.
