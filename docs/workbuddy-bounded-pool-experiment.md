# Experimental bounded SQL pool retention

This opt-in experiment is not release-ready and does not provide distributed
fencing or immediate credential revocation. It is limited to the isolated
WorkBuddy MySQL/PostgreSQL mode, with one authoritative MySQL metadata primary,
no unrestricted writers, and no native executors. It retains the separate
singleton experiment and is disabled unless the update runtime is enabled.

Before obtaining a shared pool, a deployment-provided checker must compare the
entire current stored source with the snapshot against the metadata primary.
Missing, changed, or unavailable metadata fails closed. A pool has an absolute
lifetime of at most 30 seconds from the start of its freshness check; the timer
closes that exact sql.DB even if another caller retains a pointer. An in-flight
build that exceeds its deadline cannot be installed. Existing cursor sessions
are not cancelled solely because a timer rotates the pool. The stored-source
connection probe also rechecks the primary and has a bounded context. The
browser probe for a new, unsaved candidate remains separate and requires its
own authorization and network policy.

This is eventual convergence, not a hard real-time guarantee. Scheduler
pauses and a process or metadata partition can extend the effective delay;
operations started after an earlier check can use the old credentials until
expiry. Already acquired sql.Conn, ongoing queries and open rows are not
revoked by closing sql.DB; server query limits and explicit source-side account
revocation remain necessary. No promise is made for unrestricted services,
other database types, shared metadata writers or previously started work.
Avoid duplicate long-lived source grants. A hard cutoff requires database-side
revocation or a fencing mechanism honored by the actual source.

Run scripts/test-workbuddy-bounded-pool.sh against its own disposable synthetic
MySQL and PostgreSQL containers. It validates two independent pools, stale
snapshot denial, an in-flight check racing rotation, old handle expiry, primary
failure and probe denial. It does not validate the complete WorkBuddy client,
TLS integration, tenant authorization, production database topologies, or the
latency and resource impact of periodic pool recreation.
