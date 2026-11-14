# MySQL and PostgreSQL host compatibility

For ordinary MySQL and PostgreSQL data sources, enter a DNS hostname or an IP
address in the host field without a port. Unicode internationalized DNS names
are converted to their IDNA ASCII form before the driver connects; TLS
certificates must be valid for that resulting hostname. The configured port is
provided separately. IPv6 literals may be entered with or without brackets.

IPv6 link-local addresses can contain a zone, such as fe80::1%eth0. For
PostgreSQL the old URL format also accepts a bracketed URI-escaped value such
as [fe80::1%25eth0], which means zone eth0. This is ambiguous with a literal
zone beginning with 25. To use a literal PostgreSQL zone named 25eth0, enter
fe80::1%25eth0 without brackets. MySQL treats the zone literally, so
[fe80::1%25eth0] means zone 25eth0.

The opt-in WorkBuddy onboarding instance has a separate fixed IPv4 egress
policy; this general driver capability does not expand that policy. Actual
connectivity depends on the local interface, routing and database settings.
