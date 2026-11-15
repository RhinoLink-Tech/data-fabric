# Data Fabric identity theme

Branded, Chinese-first Keycloak login and email pages for the WorkBuddy
identity stack (#618). The theme covers the two pages users see most —
sign-in and registration — the identity emails (verification, password
reset), plus one stylesheet and message overrides that restyle every other
base page (password reset, required action, error pages).

Layout and copy: warm cream canvas, single white card, terracotta accent,
serif headline. The registration form keeps only the fields a signup
actually requires: with the default username mode that is username, email,
password, confirmation; with `registrationEmailAsUsername` enabled the email
field doubles as the username (posted as `email`, synced server-side, exactly
like the base theme in that mode).

## What is overridden

```
data-fabric/
  login/
    theme.properties                  parent=base + df-* class wiring + one stylesheet
    login.ftl                         登录页 (zh labels; social/external IdP links preserved)
    register.ftl                      注册页 (required fields only; both username modes)
    resources/css/df.css              all styling, for overridden and base pages alike
    messages/messages_en.properties   zh copy for the most visible base messages
  email/
    theme.properties                  parent=base
    html/template.ftl                 branded email layout (inline styles)
    messages/messages_en.properties   zh subjects and bodies (placeholders preserved)
```

Everything else (reset-credentials, required-action, action-token, error
pages, shared JS, text-only email variants) falls through to the Keycloak
`base` theme and inherits the same classes, so those pages share the look
automatically. The base `socialProviders` section is kept in `login.ftl`, so
configured external identity providers keep appearing when this theme is
selected.

## Deployment

1. Mount the theme directory into the identity container so Keycloak sees it
   as theme name `data-fabric`:

   ```yaml
   # docker-compose volume entry
   - ./deploy/mcp/identity-theme/data-fabric:/opt/keycloak/themes/data-fabric:ro
   ```

2. In the Keycloak admin console: **Realm Settings → Themes** — set both
   **Login Theme** and **Email Theme** to `data-fabric`. Adjust **Realm
   Settings → General → Display Name** if the header wordmark should say
   something else.

3. Make the registration form actually minimal. The template no longer
   renders `firstName`/`lastName`, but Keycloak still rejects the POST until
   those attributes are **removed from the realm user profile**. On 26.3.5
   every lighter variant was tested and still enforced at registration:
   `required.scopes: []`, `required.scopes: ["profile"]`, and removing the
   `required` key while keeping the attributes — the four-field POST only
   goes through once the attributes are deleted:

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
   lastName → Delete**.

   Impact and reversibility before doing this:

   - Stored values on existing users are **not erased** — deleting the
     schema entry leaves their saved `firstName`/`lastName` in the database;
     only the managed profile stops exposing the fields.
   - Integrations or exports that read names through the user-profile API or
     the account console lose those fields after the change; export first if
     the data matters.
   - The change is reversible: re-adding the two attributes restores the
     schema, and previously stored values become visible again.

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
- **Email placeholders**: the zh overrides in `email/messages` must keep the
  base templates' `{0}`…`{n}` placeholders; re-diff against the base bundle
  when upgrading.
- **Keycloak upgrades**: templates here mirror base 26.3.5. On a major
  upgrade, re-diff against the new base `login.ftl` / `register.ftl` and the
  email bundle.
- **Acceptance script contract**: form ids `kc-form-login` /
  `kc-register-form`, the registrations link, and the
  `/login-actions/reset-credentials` link must stay; they are what
  `scripts/test-workbuddy-self-service-identity.py` parses.

## Local verification

Verified end to end on the pinned image (26.3.5):

- default username mode: four-field registration POST returns 302, the user
  is created, and a login with the new credentials returns the OAuth
  callback code
- `registrationEmailAsUsername` mode: the single email field (posted as
  `email`) registers successfully with username synced to the email
- a configured external identity provider renders in the login page's social
  section alongside the native form
- email verification: the themed verification email (zh subject, branded
  card, 验证邮箱 button) arrives via SMTP; clicking through the action-token
  confirmation flips the user to `emailVerified: true`
- password reset: the themed reset email (zh subject 重置你的密码, branded
  card, 重置密码 button) arrives with a valid action-token link

Keycloak session cookies are `Secure`, so smoke-test over TLS (self-signed
dev cert), not plain HTTP, and point the realm SMTP at a local sink for the
email journey:

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
