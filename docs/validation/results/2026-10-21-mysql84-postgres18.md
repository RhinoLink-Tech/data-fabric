# Real Source Validation Run — 2026-10-21

**Engines:** MySQL 8.4.11, PostgreSQL 18.6 (ephemeral Docker, arm64)<br>
**Harness:** scripts/validation/real_source_validation.go, run tag tn9f2r311<br>
**Result:** 18/18 checks passed (exit 0)<br>
**Wrapper:** scripts/validation/run_real_source_validation.sh

Verbatim harness output:

```text
data-fabric real-source validation (run tag tn9f2r311, engines mysql,postgres)

== mysql (127.0.0.1:32782/data_fabric_validation, reader df_reader) ==
  fixtures ready in 0.3s: dfval_tn9f2r311_main (50k rows), dfval_tn9f2r311_types, dfval_tn9f2r311_sacr
  [PASS] connect        8.4.11 (6ms)
  [PASS] read-only      INSERT rejected (read-only transaction); reads still work
  [PASS] parameters     int→1 rows, string→1 rows, injection→0 rows
  [PASS] pagination     type1 single-shot: 10 rows, no session, totalCount=30; type2 paginated: 30 rows / 3 pages of 10, ids 0..29 in order (3ms total)
2026/10/21 21:35:15 stream session cleanup failed for session d1585136-66be-450a-b532-aca3428dc6e6 (stream stream_1792589715540321000): close stream stream_1792589715540321000 rows: context canceled
  [PASS] streaming      30 rows / 3 streamed batches, ids 0..29 in order
  [note] type-mapping divergence: c_bigint pagination=bigint streaming=integer
  [note] type-mapping divergence: c_text pagination=text streaming=string
  [note] type-mapping divergence: c_dt pagination=datetime streaming=date
  [PASS] types          10 columns exact in both modes: c_tinyint=integer,c_int=integer,c_bigint=bigint,c_dec=decimal,c_vc=string,c_text=text,c_date=date,c_dt=datetime,c_bool=integer,c_null=integer
  [PASS] cancellation   pagination canceled at 0.70s [The query was cancelled before completion.]; streaming canceled at 0.70s [The query was cancelled before completion.]
  [PASS] error-mapping  syntax=stable, missing-table=stable, non-select=stable
  [PASS] isolation      interleaved 20+20 rows, A ascending=true B descending=true, bad-creds=stable

== postgres (127.0.0.1:32784/data_fabric_validation, reader df_reader) ==
  fixtures ready in 0.1s: dfval_tn9f2r311_main (50k rows), dfval_tn9f2r311_types, dfval_tn9f2r311_sacr
  [PASS] connect        PostgreSQL 18.6 (Debian 18.6-1.pgdg13+2) on aarch64-unknown-linu… (6ms)
  [PASS] read-only      INSERT rejected (read-only transaction); reads still work
  [PASS] parameters     int→1 rows, string→1 rows, injection→0 rows
  [PASS] pagination     type1 single-shot: 10 rows, no session, totalCount=30; type2 paginated: 30 rows / 3 pages of 10, ids 0..29 in order (7ms total)
  [PASS] streaming      30 rows / 3 streamed batches, ids 0..29 in order
  [note] type-mapping divergence: c_text pagination=text streaming=string
  [note] type-mapping divergence: c_ts pagination=timestamp streaming=time
  [PASS] types          10 columns exact in both modes: c_small=integer,c_int=integer,c_bigint=integer,c_dec=decimal,c_vc=string,c_text=text,c_date=date,c_ts=timestamp,c_bool=boolean,c_null=integer
  [PASS] cancellation   pagination canceled at 0.71s [The data-source query failed. Check the SQL dialect, catalog and schema, permissions, and query-engine availability.]; streaming canceled at 0.70s [The query was cancelled before completion.]
  [PASS] error-mapping  syntax=stable, missing-table=stable, non-select=stable
  [PASS] isolation      interleaved 20+20 rows, A ascending=true B descending=true, bad-creds=stable

result: all checks passed (exit 0)
```
