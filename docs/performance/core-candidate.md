# Experimental lightweight connector build

Tracking: [#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448). **This is an engineering candidate, not a supported or published release profile.** The standard build remains the full registry.

A binary built with `-tags datafabric_core` registers MySQL, MariaDB, PostgreSQL and Trino. Other source types are not present in this registry; runtime source discovery reports the active build's catalog. Trino is a gateway connection to a separately deployed federation engine, not an engine embedded in the candidate. Python SQL translation still needs its separately packaged runtime.

The Dockerfile accepts `DATA_FABRIC_BUILD_TAGS=datafabric_core` for isolated candidate testing; an omitted build argument retains the standard registry. The label `tech.rhinolink.data-fabric.build-tags` records the selected tag. A source build can use `go build -tags datafabric_core ./cmd/server` when all Go dependencies and the toolchain are already cached. Docker's base images and dependencies may be large; the repository's candidate workflow builds remotely so local validation does not require pulling them.

The candidate workflow compares full and candidate Docker image sizes and gzip-compressed `docker save` archives, then exercises the same synthetic MySQL/PostgreSQL join, paginated REST and MCP results, and policy denial against the candidate image. A compressed Docker archive is not a registry transfer size. Go/security and MCP catalog checks run with the build tag; the standard profile remains covered by normal CI.

Before any supported release, record artifact digests, exact workflow run and source SHA; extend tests to the intended browser onboarding, MariaDB, SQL translation, recovery, policy revocation and audit behavior; measure startup and whole-stack idle resources; and document migration and support for excluded sources. Existing data-source registrations for omitted types do not become available automatically. See the [offline registry size experiment](restricted-registry-experiment.md) for its earlier binary-only measurements.
