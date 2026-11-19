# Restricted driver registry: offline size experiment

Status: **experimental measurement, not a released distribution**. Tracking: [#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448).

## Why

A maintainer explicitly requested simpler deployment and avoidance of large local image downloads. Existing [deployment-footprint measurements](deployment-footprint.md) measured a full image; they did not establish whether a restricted connector set materially reduces its executable. This experiment compares binaries without pulling or building Docker images.

## Candidate and result

Measured 2026-11-19 on Linux/amd64 with Go 1.26.1. The Go source matches master commit [`ee3c21a`](https://github.com/RhinoLink-Tech/data-fabric/commit/ee3c21a9e8cecd7b69c8f301d8775b01401f84c5); the measured local tree `a18d07bd73f4cac2bb2fa9b70bdd8552268aabac` additionally contains this experiment script and documentation. The full baseline uses the existing registry. The first candidate registers MySQL, MariaDB and PostgreSQL; the second adds Trino. Each candidate temporarily replaces only `registerDrivers` and its imports. Neither alters the checkout or creates a release profile.

Both builds use `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=amd64`, `-buildvcs=false`, `-trimpath` and `-ldflags="-s -w"`. The local module/build cache was already warm. Each variant was built twice in order, full then three-type then Trino; the figures are observations, not a controlled cold-build benchmark.

| Measurement | Full registry | Three-type candidate | Candidate with Trino |
| --- | ---: | ---: | ---: |
| Go server binary | 127,119,522 bytes (121.23 MiB) | 99,287,202 bytes (94.69 MiB) | 108,421,282 bytes (103.40 MiB) |
| Binary gzipped at level 6 | 36,388,528 bytes (34.70 MiB) | 27,970,186 bytes (26.67 MiB) | 30,695,643 bytes (29.27 MiB) |
| First / second build | 3.380 / 0.190 s | 1.306 / 0.180 s | 2.315 / 0.190 s |

The three-type executable is 26.54 MiB smaller (21.9%). The candidate retaining Trino is 17.83 MiB smaller (14.7%); its gzip is 15.6% smaller. **Gzip of one executable is not a container registry transfer or image-size measurement.** Timing is cache- and order-dependent; no startup time, RSS, actual MySQL/PostgreSQL queries, workload or end-to-end release contract was measured here.

Limiting registration alone does not necessarily remove all nonselected SDKs: the health checker and metadata extractor directly import file-system and object-storage packages. The three-type candidate excludes Trino, so the existing Trino-based cross-source route would not be available. The second registers Trino but does not validate federation or deploy an external engine. SQL translation also depends on the separate Python runtime, which this binary-only experiment does not package or validate.

## Decision and next gate

The measured saving justifies investigating an **optional** smaller distribution. It does not justify replacing the full default, claiming a smaller published image, or advertising reduced runtime memory. Before a supported profile: select exact connector contents including how federation is enabled; isolate all relevant imports and report actual catalog availability; build both images in isolated remote CI; measure compressed transfer, uncompressed image, build time, startup and whole-stack idle resources; then run the same REST/MCP, SQL translation, authorization, audit, continuation and selected live-source contracts against both artifacts. Document migration and support cost for excluded sources. No existing release is changed by this experiment.

## Repeat offline

With Python 3.12+ and a local Go 1.26.1+ installation plus the repository's cached dependencies:

```bash
python3 scripts/performance/driver_registry_size_probe.py --go /path/to/go1.26.1/bin/go
```

The script archives the committed checkout to a temporary directory, builds the full, three-type and Trino registry variants twice, gzip-compresses the binaries and prints JSON with sizes and times. `GOPROXY=off`, `GOSUMDB=off` and `GOTOOLCHAIN=local` prevent dependency and toolchain downloads. Missing cached dependencies fail the run. It uses no Docker, does not modify the checkout and removes its temporary source and binaries. The committed tree, Go version, host cache state and build flags must accompany any new result.
