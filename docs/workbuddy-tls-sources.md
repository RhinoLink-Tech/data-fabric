# WorkBuddy: verified TLS data sources

The restricted WorkBuddy source wizard supports PostgreSQL and MySQL over TLS.
Select TLS and choose either system trust or the operator-provisioned custom
CA. The host supplied in the wizard must match the server certificate SAN.
The service resolves the name during each test or creation, checks the selected
IPv4 address and port against its operator allowlist, then stores that fixed IP
with the original certificate name. It does not follow subsequent DNS changes;
reconnect the source when its approved address changes.

To configure one custom CA, mount an administrator-controlled PEM bundle as a
regular file in the service container and set
DATA_FABRIC_WORKBUDDY_CA_FILE to its container path. Restart all service
instances with the same CA and network policy after configuration changes. The
browser accepts only the trust alias custom or system, never a filesystem
path, private key, weak TLS mode, or arbitrary server-name override. The custom
trust roots are loaded into memory at startup. Custom CA loading currently requires
a Unix platform with secure no-follow, non-blocking file opening; other
platforms fail closed and can use system trust.

The wizard currently covers MySQL and PostgreSQL and IPv4 destinations only.
It does not support client-certificate authentication. Non-TLS connections are
still supported for environments that explicitly permit them. Database-side
credentials and grants determine operations the user may perform.

Run scripts/test-workbuddy-tls.sh with Docker, OpenSSL, and Go installed to
exercise isolated synthetic TLS-enabled PostgreSQL and MySQL containers. The
script only creates its uniquely named test containers and temporary synthetic
certificates.
