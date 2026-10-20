# Ingestion validation: migrations, load, recovery and late data

Evidence for rolling out idempotent, crash-safe position ingestion (`docs/position-batch.md`), and what still
needs staging or a business decision. Everything here ran locally against disposable PostgreSQL clusters;
nothing touched production.

## 1. Migrations

### Production facts used

From the read-only inventory of 2026-10-15 (`server-go/inventory/results/2026-10-15.md` on `go-migration`):
RDS **PostgreSQL 16.13**, **no TimescaleDB**, `databasechangelog` with 58 rows (25 legacy `MARK_RAN` rows plus
everything through `changelog-6.13.0` and `changelog-6.13-dsc`), `tc_positions` 17.6M rows / 5.1 GB,
`tc_events` 556k, 1,038 devices, 2,253 geofences (212 longer than 4,096 characters), 21,491 device-geofence
links, no linked devices. The `tc_actions` row count was not recorded.

Re-check before rollout (read-only):

```sql
SELECT version();
SELECT extname, extversion FROM pg_extension;                 -- expect no timescaledb
SELECT id, md5sum FROM databasechangelog ORDER BY orderexecuted;  -- compare with the table below
SELECT count(*) FROM tc_actions;                               -- sizes the 6.15.0 lock window
SELECT count(*) FROM tc_device_device;                         -- 6.13.0-fk-linkeddeviceid validates these
SELECT count(*) FROM tc_device_device dd LEFT JOIN tc_devices d ON d.id = dd.linkeddeviceid WHERE d.id IS NULL;
                                                               -- must be 0, or the new foreign key fails
```

### Pending changesets

Deploying this branch's `schema/` makes Liquibase run, at server start, exactly:

| Changeset | What it does | Locks | Measured (PostgreSQL 16.13) |
|---|---|---|---|
| `changelog-6.13.0-fk-linkeddeviceid` (upstream) | FK `tc_device_device.linkeddeviceid` → `tc_devices` | SHARE ROW EXCLUSIVE on both tables while validating | 74–97 ms (empty `tc_device_device`) |
| `changelog-6.15.0` (upstream) | `int` → `bigint` for `tc_devices.positionid/motionpositionid/overspeedgeofenceid`, `tc_actions.userid/objectid` | **ACCESS EXCLUSIVE on `tc_devices` and `tc_actions`**, both rewritten, for the whole changeset | 1.5–4.0 s with 500k `tc_actions` rows (scales with that count) |
| `changelog-6.16-dsc-ingest` | creates `tc_position_receipts` | none on existing tables (FK reference only) | 18–156 ms |
| `changelog-6.16-dsc-outbox` | creates `tc_outbox`, `tc_position_quarantine`, `tc_late_positions` | none on existing tables | 44–187 ms |
| `changelog-6.16-dsc-outbox-claim` | adds nullable `tc_outbox.claimtoken` | ACCESS EXCLUSIVE on the new, empty `tc_outbox`; no rewrite | 4–7 ms |
| `changelog-6.16-dsc-device-geofence-index` | index on `tc_device_geofence(deviceid)` | SHARE on `tc_device_geofence` (writes to links wait) | 20–23 ms (21,491 links) |

A probe writing like the server (insert a position, move the device's latest position, insert an action every
10 ms) was blocked for up to 3,977 ms during `6.15.0`; nothing else blocked it beyond 100 ms. `tc_positions`
(5.8 GB in the test) is not touched by any pending changeset.

**Maintenance window:** the migration runs when the new server starts, before it accepts traffic, so with one
server the outage is the restart plus the migration (seconds, dominated by the `tc_actions` rewrite). With the
queue in front, reports wait in SQS meanwhile. Stop all old servers first: an old server writing during
`6.15.0` would just block, but two different server versions must not process the same devices.

### Results

`./gradlew migrationCheck -PpgBin=<postgresql-16.13>/bin [-Ppositions=17600000] [-Pactions=500000]` builds a
production-shaped database (the production changelog history, no TimescaleDB, synthetic data with production
counts), then:

- **Pending list** = the six changesets above, nothing else.
- **Second run:** 0 changesets executed, schema identical (columns, indexes, constraints compared).
- **Startup and ingestion** after migration: a batch is stored with its receipt and outbox entry, and becomes
  the device's latest position.
- **History shape:** 33 rows, i.e. production's 58 without its 25 legacy rows.

### Checksums

Liquibase refuses to start if a stored checksum differs from the changelog's. These are the values the
changelogs produce now (Liquibase 5.0.3); production's stored values for the already applied rows must match:

| id | md5sum |
|---|---|
| `changelog-4.0-clean` | `9:29260b7c580835a4006cb8c8303f753e` |
| `changelog-4.0-clean-common` | `9:9106689cc508433f83e5bf7885bfc276` |
| `changelog-4.1-mssql` | `9:0b4cd69b7025008e34cc0b8439f1d905` |
| `changelog-4.7` | `9:828c072f51d04a38470dd96399de6233` |
| `changelog-4.9` | `9:62f4b0b8b3ec5c8746ed1ac842107e79` |
| `changelog-4.10` | `9:3602f5246bfb9c2323e0ca67cfdab70a` |
| `changelog-4.11` | `9:e9052c48b7d99aa9738ab35ba314929f` |
| `changelog-4.13` | `9:6feeb695221e4b844967cec335fb6aea` |
| `changelog-4.15` | `9:c7e7a54391ed2e16ef5a90fd68c0f9d7` |
| `changelog-5.0` | `9:ac3942110503f904188c67b63fc7c072` |
| `changelog-5.1` | `9:82263460dd8dd7e71eba9c368bf5a74b` |
| `changelog-5.2` | `9:c1c7f476b08ce0a19801431add8446fd` |
| `changelog-5.3` | `9:1a574cf5780301ad3488b68d7e457e27` |
| `changelog-5.4` | `9:f3048803607da81d070ff76022d1020a` |
| `changelog-5.5` | `9:23b37a6f82fd32497af31f093b855dc9` |
| `changelog-5.6` | `9:99240a2ac8883cb74c5cba38ef5c1686` |
| `changelog-5.7` | `9:f2425b5cceff33f0abbe4c1ea0c18a28` |
| `changelog-5.8` | `9:6700eee6204986c4db1134cf30a188d6` |
| `changelog-5.9` | `9:f516b72a9e6c82f09e8bbf7802884768` |
| `changelog-5.10` | `9:2de9e66d1f8219a42b14390dd5a5b218` |
| `changelog-5.11` | `9:f30849022d894f99ea42f93e69c00ed8` |
| `changelog-6.2` | `9:4afc10f18622be53ad650af7d23ca1e0` |
| `changelog-6.3-old` | `9:4e1d3dc09c7c412a7637461af4f40931` |
| `changelog-6.3-new` | `9:1ecbc64d93f7dcb304d5f482b04b9b56` |
| `changelog-6.6` | `9:9de8626a90708711843aab4560a33515` |
| `changelog-6.7.0` | `9:c50bbf6b813b73fb4f0c05fcf44138c5` |
| `changelog-6.8.0-timescale` | `9:637e423153d145a711b105077f8a6a5f` |
| `changelog-6.9.0` | `9:90ad1e34795d2cff993fd4f58c2f8b10` |
| `changelog-6.11.0-timescale` | `9:662d571b2402d01cf9b9cdf935ec3e2c` |
| `changelog-6.11.0-other` | `9:c9ce187cebe6eef9470746f77eb2b9d9` |
| `changelog-6.11.0` | `9:47b9f7267ee18f5e95449a3b42c68e2f` |
| `changelog-6.13.0` | `9:684efa3c5b279fbdcddb71c6cb754c4c` |
| `changelog-6.13-dsc` | `9:211c8fba977bcf7a198ab339f82c189f` |

Written by this migration:

| id | md5sum |
|---|---|
| `changelog-6.13.0-fk-linkeddeviceid` | `9:b4cb8d4a152cd5b65ab0077e70663916` |
| `changelog-6.15.0` | `9:982cf8cf45bcf15d379cace5bbeee7f1` |
| `changelog-6.16-dsc-ingest` | `9:f5fbc3d78c15bfa9696755d08d8bc2de` |
| `changelog-6.16-dsc-outbox` | `9:968bf951d9a79a1502ca40c9026a7463` |
| `changelog-6.16-dsc-outbox-claim` | `9:b75954497641b8fb072b34acb6275231` |
| `changelog-6.16-dsc-device-geofence-index` | `9:18f21f785d2bf6567c243c9449762ffd` |

### Backup, rollback, downgrade

- **Backup:** take an RDS snapshot right before deploying (restore = new instance from the snapshot, then point
  the servers at it). Not exercised here: no RDS access.
- **Downgrading the binary does not undo the schema.** Tested: the current `master` jar (production's code)
  started on the migrated database, applied nothing ("Database is up to date"), stored a live position and
  made it the device's latest. The extra tables are ignored; the `bigint` columns are read as before. While
  the old binary runs: no receipts (duplicates are stored again), no outbox (deliveries it would have queued
  are sent directly, at most once, as before), current position not guarded against late reports.
  **Before downgrading, wait until `SELECT count(*) FROM tc_outbox WHERE deliveredat IS NULL` is 0**, or those
  webhooks and position forwards are never sent.
- **Schema rollback** (only if needed; not required for the old binary):
  `DROP TABLE tc_position_receipts, tc_outbox, tc_position_quarantine, tc_late_positions;
  DROP INDEX idx_device_geofence_deviceid;` and delete their four `databasechangelog` rows
  (`changelog-6.16-dsc-*`). `6.15.0` and the linked-device FK are upstream and have no rollback; reverting
  them means a rewrite of the same tables, as long as the forward one.

### TrackHub

All 18 EF migrations, including `AddWebhookPushJobs` and `AddWebhookPushJobClaimToken`, applied on
PostgreSQL 16.13 with `dotnet ef database update`; a second run applied nothing. The new migrations create one
table (empty, with an index) and add a nullable column without default: no rewrite, no lock on existing
tables. Generate the reviewable script with
`dotnet ef migrations script 20261013022723_AddStationaryAccuracyToDeviceState --idempotent`. TrackHub's
production database version and applied migrations were not inspected.

## 2. Load and recovery

`./gradlew loadTest -PpgBin=<postgresql-16>/bin` runs, in one JVM, N servers sharing a disposable PostgreSQL
(`fsync` and `synchronous_commit` on), synthetic devices reporting through the batch endpoint like the queue
consumer (one report per device per interval, routed by device to a server), 20 geofences per device crossed
periodically, a share of devices re-sending shuffled offline batches (duplicates of live reports plus reports
never sent live, i.e. late), a server crashed and restarted, and a webhook outage. Unfinished items are queued
and retried like SQS messages. Every 5 s it prints offered / acknowledged / **durable** (rows committed)
positions per second, acknowledgement latency percentiles, client backlog, server processing queue, pool use,
database transactions, CPU and I/O, lock waits, outbox backlog and oldest age, webhook calls, refusals and
failures; at the end, exactly-once checks and the top statements from `pg_stat_statements`.

Options (`-P`): `devices` (20000), `interval` (5 s), `duration` (300 s), `instances` (2), `geofences`
(2253), `linksPerDevice` (20), `crossingSeconds` (600), `offlineShare` (0.02), `offlineEvery` (60 s),
`killAt` (120 s), `restartAt` (180 s), `webhookDownFrom`/`webhookDownTo` (60/90 s), `maxInFlight` (2000),
`workers` (16 per server), `pool` (40 per server).

### Measured locally (12 cores, 15 GB, one NVMe; PostgreSQL, servers and generator on the same machine)

| Run | Result |
|---|---|
| 2,000 devices (400/s), 60 s, crash + restart | 400/s durable, p99 acknowledgement < 120 ms except the first 5 s; 24,000 reports → 24,000 rows and receipts; crash and restart without a dip |
| 20,000 devices (4,000/s), stopped by request during the drain | **~3,000/s durable plateau**, p50 ~0.6 s; the client backlog grew to ~455k items and drained at ~3,000/s after traffic stopped; server crash: ~10 s dip, then the single survivor held ~3,000/s; 938 transport errors and 493 failed items, all retried |

The 4,000/s target was **not** reached here. At the plateau PostgreSQL used ~2.6 of 12 cores, ~150 MB/s of
writes, no lock waits, and ~25 of 80 pool connections, while each server's processing queue held ~1,000
positions: the limit was processing concurrency (16 workers × 2 servers, one synchronous transaction of
~10 ms each), not the database. Raising `processing.workers` (and `database.maxPoolSize` with it) was the next
experiment; it was not run. The exactly-once summary of the 20,000-device run did not run (stopped).

Found and fixed by the load test: loading a device into a server's cache read **every** link of the fleet
(`SELECT * FROM tc_device_geofence`, filtered in Java), once per device and server; at 2,000 devices it was
the most expensive statement and made first contact and failover take 10+ s. It now filters by id in the
database, with a new index. This is upstream Traccar behaviour.

Not measured: the webhook outage (no event fell in the window; crossings every 5 min), a delivery worker
crash under load, memory under sustained backlog beyond the run, and anything on production-like hardware.

### Staging run

On hardware like production's (separate database host), with the staging database restored from a snapshot:

```
./gradlew loadTest -PpgBin=/usr/lib/postgresql/16/bin -Pdevices=20000 -Pduration=1800 \
    -Pworkers=48 -Ppool=64 -PcrossingSeconds=120 -PkillAt=600 -PrestartAt=900 \
    -PwebhookDownFrom=300 -PwebhookDownTo=420
```

(the harness starts its own cluster from `pgBin`; to measure a separate database host, run the servers against
it instead.) Pass when: durable rate ≥ offered in steady state, the backlog after the crash drains while
traffic continues, receipts = stored positions = distinct reports, the outbox returns to 0 after the webhook
outage, and no `lock` failures.

## 3. Late data

Late positions are stored, never become the current position, and each records the interval it changes in
`tc_late_positions` (`fromtime`/`totime` = stored neighbours, `completedat` NULL). **No reconstruction job
exists**: nothing sets `completedat`.

`docs/late-data-incomplete.sql` lists, per device and merged interval, what may be incomplete, with the derived
events inside it; its second query tells whether a report range is affected. It is covered by
`LateDataVisibilityTest`. Inside an interval: geofence visits are reported (marked `late`), motion, overspeed
and ignition events are not recomputed, trips/stops over more than one day (which read motion events) can be
wrong, summary distance can undercount.

**Business decision needed:** may the system go live with late data only *flagged* (geofence visits recovered,
other derived events not), or is a reconstruction job a rollout prerequisite? The original acceptance criteria
required not silently losing meaningful events: geofence events are recovered and every other gap is now
visible and queryable, but not corrected. No UI shows it yet (sir-web-react was not changed).
