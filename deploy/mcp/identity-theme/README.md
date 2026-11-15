# Data Fabric identity login theme

Branded, Chinese-first Keycloak login pages for the WorkBuddy identity stack
(#618). The theme covers the two pages users see most — sign-in and
registration — plus one stylesheet and message overrides that restyle every
other base page (password reset, required action, error pages).

Layout and copy: warm cream canvas, single white card, terracotta accent,
serif headline. The registration form keeps only the fields a signup actually
requires: username, email, password, confirmation.

## What is overridden

```
data-fabric/login/
  theme.properties                  parent=base + df-* class wiring + one stylesheet
  login.ftl                         登录页 (zh labels, required-only form)
  register.ftl                      注册页 (username / email / password / confirm)
  resources/css/df.css              all styling, for overridden and base pages alike
  messages/messages_en.properties   zh copy for the most visible base messages
```

Everything else (reset-credentials, required-action, action-token, error
pages, shared JS) falls through to the Keycloak `base` theme and inherits the
same `df-*` classes, so those pages share the look automatically.

## Deployment

1. Mount the theme directory into the identity container so Keycloak sees it
   as theme name `data-fabric`:

   ```yaml
   # docker-compose volume entry
   - ./deploy/mcp/identity-theme/data-fabric:/opt/keycloak/themes/data-fabric:ro
   ```

2. In the Keycloak admin console: **Realm Settings → Themes → Login Theme →
   `data-fabric`**, then **Realm Settings → General → Display Name** if the
   header wordmark should say something else.

3. Make the registration form actually minimal. The template no longer
   renders `firstName`/`lastName`, but Keycloak still rejects the POST until
   those attributes are **removed from the realm user profile** — marking
   them optional is not enough (verified on 26.3.5: `required.scopes: []`
   and `required.scopes: ["profile"]` are still enforced at registration):

   ```bash
   # fetch, delete the firstName/lastName attribute entries, PUT back
   curl -s -H "Authorization: Bearer $TOKEN" \
     "$KC/admin/realms/data-fabric/users/profile" > profile.json
   # edit profile.json: remove the objects named firstName and lastName
   # from the "attributes" array
   curl -s -X PUT -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
     -d @profile.json "$KC/admin/realms/data-fabric/users/profile"
   ```

   Admin-console path: **Realm Settings → User Profile → firstName /
   lastName → Delete** (delete, not just uncheck Required).

4. Optional, for remaining system messages (verification emails, validation
   errors on pages this theme does not template-override): set the realm
   default locale to `zh-CN`.

## Maintenance notes

- **Terms & conditions / reCAPTCHA**: the custom `register.ftl` does not
  render the terms checkbox or reCAPTCHA blocks. Enable either on the realm
  and this template must be extended first (see base `register.ftl` for the
  macros to copy in).
- **Password visibility toggle** is intentionally omitted (plain password
  inputs) to keep the page minimal.
- **Keycloak upgrades**: templates here mirror base 26.3.5. On a major
  upgrade, re-diff against the new base `login.ftl` / `register.ftl`.
- **Acceptance script contract**: form ids `kc-form-login` /
  `kc-register-form`, the registrations link, and the
  `/login-actions/reset-credentials` link must stay; they are what
  `scripts/test-workbuddy-self-service-identity.py` parses.

## Local verification

Verified end to end on the pinned image (26.3.5): with the theme selected and
the two name attributes removed, a four-field registration POST returns 302,
the user is created, and a login with the new credentials returns the OAuth
callback code. Keycloak session cookies are `Secure`, so smoke-test over TLS
(self-signed dev cert), not plain HTTP:

```bash
openssl req -x509 -newkey rsa:2048 -keyout tls.key -out tls.crt -days 2 -nodes \
  -subj '/CN=127.0.0.1' -addext 'subjectAltName=IP:127.0.0.1'
docker run --rm -p 127.0.0.1:8443:8443 \
  -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=... \
  -v "$PWD/deploy/mcp/identity-theme/data-fabric:/opt/keycloak/themes/data-fabric:ro" \
  -v "$PWD/tls.crt":/etc/keycloak/tls.crt:ro -v "$PWD/tls.key":/etc/keycloak/tls.key:ro \
  quay.io/keycloak/keycloak@sha256:357829ec7c4693397533035092ad13b0644bcc95ded311f33a3738c4d9e9bdba \
  start-dev --http-relative-path=/identity \
  --https-certificate-file=/etc/keycloak/tls.crt --https-certificate-key-file=/etc/keycloak/tls.key
# then create a test realm with "loginTheme":"data-fabric", delete the
# firstName/lastName profile attributes, and open
# https://127.0.0.1:8443/identity/realms/<realm>/protocol/openid-connect/auth?...
```
