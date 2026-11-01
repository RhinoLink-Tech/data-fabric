# Deployment Footprint: Before and After Release Stripping

**Measured:** 2026-11-01

**Baseline source commit:** [a03e1988](https://github.com/RhinoLink-Tech/data-fabric/commit/a03e1988fafd28bce72fae5f7e043b06c30f1c4d)

**Stripped release build commit:** [ee206256](https://github.com/RhinoLink-Tech/data-fabric/commit/ee206256bd0848b9c8601eb2fd6a5de8241e9061)

**Go builder:** `golang:1.26.1`

This report compares the release Docker image before and after applying Go linker stripping. The two runs used the same WSL2 host, pinned Go builder, pinned MySQL image, and workload. No Go application source files changed between the measured revisions.

## Environment

| Item | Measured environment |
| --- | --- |
| Host CPU | Intel Core Ultra 9 275HX |
| Logical CPUs visible to WSL | 24 |
| Host memory visible to WSL | 16,466,497,536 bytes (about 15.34 GiB) |
| OS | Ubuntu on WSL2, Linux 6.18.33.2-microsoft-standard-WSL2, x86_64 |
| Docker Engine | 29.8.0 |
| Go builder | `golang:1.26.1`, pinned in the Dockerfile |
| Synthetic source and metadata DB | MySQL 8.4.13, image digest `mysql@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7` |
| Data Fabric mode | release |
| Application container memory limit | None |

The disposable MySQL instance stores Data Fabric metadata in one database and the synthetic source table in another. Database CPU and memory are not included in Data Fabric RSS measurements.

## Before and after

| Measurement | Unstripped baseline | Stripped release build | Change |
| --- | ---: | ---: | ---: |
| Release server binary | 164.2 MiB (172,161,122 bytes) | 119.5 MiB (125,284,514 bytes) | −44.7 MiB (−27.2%) |
| Final Docker image, uncompressed | 311.0 MiB (326,102,374 bytes) | 236.1 MiB (247,649,133 bytes) | −74.8 MiB (−24.0%) |
| First start to healthy readiness, including migrations | 0.600 s | 0.578 s | −0.022 s |
| Restart to healthy readiness, 5 samples, median (range) | 0.493 s (0.482–0.521 s) | 0.488 s (0.474–0.525 s) | −0.005 s |
| Idle process RSS after 5 seconds, 5 samples, median (range) | 82.65 MiB (82.51–83.52 MiB) | 83.61 MiB (82.29–83.85 MiB) | +0.96 MiB |
| Small one-row query process high-water RSS, 3 samples, median (range) | 84.43 MiB (83.88–84.76 MiB) | 85.22 MiB (84.13–86.08 MiB) | +0.79 MiB |
| Bounded stream process high-water RSS, 3 samples, median (range) | 95.64 MiB (95.35–97.78 MiB) | 95.09 MiB (95.06–95.70 MiB) | −0.55 MiB |

The bounded stream returned 50,000 rows with a 256-byte payload in batches of 1,000 rows (50 HTTP requests). RSS is the Data Fabric container PID 1 measurement from `/proc/1/status`. VmHWM is the process high-water mark since process start, not an isolated per-query allocation measurement. The database containers and load-generating client are outside the Data Fabric container. No concurrent client load was applied.

The small-query high-water median was 0.79 MiB higher in the optimized run; idle RSS was 0.96 MiB higher, while the bounded-stream high-water median was 0.55 MiB lower. These small absolute differences do not show a material streaming-memory regression, but the measurements do not establish a causal runtime-memory improvement from linker stripping. Linker stripping is an artifact-size optimization, not a query-acceleration claim.

A separate smoke check built a small Go program with the same builder and `-s -w` flags, triggered a panic, and confirmed the runtime output retained the function name and source line. The release binary omits the ELF symbol table and DWARF debugging data; use a separately retained symbolized build if native debugger or core-dump symbolization is needed.

The image size is Docker's uncompressed local image size, not the compressed registry transfer size. The test uses one machine, one MySQL version, one source shape, one request at a time, and no container memory cap. Do not extrapolate these values to other hardware, providers, concurrency levels, or production deployments.

## Raw data

- Baseline: [CSV samples](results/deployment_footprint_2026-11-01.csv), [environment metadata](results/deployment_footprint_2026-11-01.json)
- Stripped release build: [CSV samples](results/deployment_footprint_stripped_2026-11-01.csv), [environment metadata](results/deployment_footprint_stripped_2026-11-01.json)

## Reproduce

Requirements: Docker, Python 3, and network access to pull the pinned MySQL image if it is not cached. Go is built inside the pinned Dockerfile builder image.

From a clean checkout, run:

~~~bash
python3 scripts/performance/deployment_footprint_benchmark.py
~~~

The runner builds the production Dockerfile, extracts the release binary to measure its size, then creates uniquely named temporary Docker network and MySQL/Data Fabric containers. It generates temporary database, JWT, and credential-vault keys; it never writes credentials to the report or sample CSV. The containers, network, generated local binary, and built Data Fabric image are removed on exit. The pinned MySQL image remains in the local Docker cache.

Defaults are 50,000 source rows, 256-byte payloads, 1,000-row stream batches, five startup samples, three query samples per workload, and a five-second idle settling period. Set `DATA_FABRIC_FOOTPRINT_ROWS` to change the row count or `DATA_FABRIC_FOOTPRINT_RAW_OUTPUT` to set the raw CSV destination. The runner writes an adjacent JSON file with the environment, source commit, build flags, and image details.
