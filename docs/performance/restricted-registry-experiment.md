# Restricted driver registry: offline size experiment

Status: **experimental measurement, not a released distribution**. Tracking: [#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448).

## Why

A maintainer explicitly requested simpler deployment and avoidance of large local image downloads. Existing [deployment-footprint measurements](deployment-footprint.md) measured a full image; they did not establish whether a restricted connector set materially reduces its executable. This experiment compares binaries without pulling or building Docker images.

## Candidate and result

Measured 2026-11-19 on Linux/amd64 with Go 1.26.1. The committed source tree was `aa2c06c962c7916402a619f87ed5f717a1b1c85e`, identical to master commit [`ee3c21a`](https://github.com/RhinoLink-Tech/data-fabric/commit/ee3c21a9e8cecd7b69c8f301d8775b01401f84c5). The full baseline uses that tree's registry. The comparison temporarily replaces only `registerDrivers` and its imports, registering MySQL, MariaDB and PostgreSQL; it does not alter the checkout or create a release profile.

Both builds use `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=amd64`, `-buildvcs=false`, `-trimpath` and `-ldflags="-s -w"`. The local module/build cache was already warm. Each variant was built twice in order, full first; the figures are observations, not a controlled cold-build benchmark.

| Measurement | Full registry | Restricted registry experiment |
| --- | ---: | ---: |
| Go server binary | 127,119,522 bytes (121.23 MiB) | 99,287,202 bytes (94.69 MiB) |
| Binary gzipped at level 6 | 36,388,528 bytes (34.70 MiB) | 27,970,186 bytes (26.67 MiB) |
| First / second build | 3.540 / 0.200 s | 1.292 / 0.187 s |

The binary is 26.54 MiB smaller (21.9%); gzip is 8.03 MiB smaller (23.1%). **Gzip of one executable is not a container registry transfer or image-size measurement.** Timing is cache- and order-dependent; no startup time, RSS, actual MySQL/PostgreSQL queries, workload or end-to-end release contract was measured here.

Limiting registration alone does not necessarily remove all nonselected SDKs: the health checker and metadata extractor directly import file-system and object-storage packages. In this experiment Trino and all other source types are absent from the registry, so the existing Trino-based cross-source route would not be available. SQL translation also depends on the separate Python runtime, which this binary-only experiment does not package or validate.

## Decision and next gate

The measured saving justifies investigating an **optional** smaller distribution. It does not justify replacing the full default, claiming a smaller published image, or advertising reduced runtime memory. Before a supported profile: select exact connector contents including how federation is enabled; isolate all relevant imports and report actual catalog availability; build both images in isolated remote CI; measure compressed transfer, uncompressed image, build time, startup and whole-stack idle resources; then run the same REST/MCP, SQL translation, authorization, audit, continuation and selected live-source contracts against both artifacts. Document migration and support cost for excluded sources. No existing release is changed by this experiment.

## Repeat offline

With Python 3.12+ and a local Go 1.26.1+ installation plus the repository's cached dependencies:

```bash
python3 scripts/performance/driver_registry_size_probe.py --go /path/to/go1.26.1/bin/go
```

The script archives the committed checkout to a temporary directory, builds the full and restricted registry variants twice, gzip-compresses the binaries and prints JSON with sizes and times. `GOPROXY=off`, `GOSUMDB=off` and `GOTOOLCHAIN=local` prevent dependency and toolchain downloads. Missing cached dependencies fail the run. It uses no Docker, does not modify the checkout and removes its temporary source and binaries. The committed tree, Go version, host cache state and build flags must accompany any new result.
