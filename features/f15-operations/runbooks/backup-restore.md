# Backup and restore runbook

This runbook implements D05. Backups and WAL archives are encrypted, isolated
from production, checksum verified, and retained for at most 35 calendar days.
Production runtime credentials must not be able to delete backup copies.

The concrete backup product and secret-manager commands are deployment choices.
Operators must use the approved provider procedure rather than placing
credentials in this repository or shell history.

## Objectives

| Component | RPO | RTO | Recovery source |
| --- | ---: | ---: | --- |
| PostgreSQL, audit, durable jobs | 15 minutes | 4 hours | Daily backup and continuous WAL/PITR |
| Media and user objects | 24 hours | 8 hours | Encrypted daily version/snapshot plus checksum inventory |
| Application and infrastructure config | 24 hours | 4 hours | Versioned code/IaC and protected bootstrap config |
| Derived Redis caches and queues | No persistence | 1 hour | Rebuild from authoritative PostgreSQL state |
| Minimum end-to-end service | Component RPO | 8 hours | API, PostgreSQL and at least one worker |

## Backup procedure

1. The scheduler starts the approved backup job with a least-privilege recovery
   identity obtained directly from the secret manager.
2. Create the daily PostgreSQL base backup while continuous WAL archiving
   remains active. Store both in a separate failure domain.
3. Create the encrypted object snapshot/version and its checksum inventory.
4. Verify backup and WAL checksums, encryption metadata, restore readability,
   age, and the automatic 35-day expiry.
5. Publish only completion time and verification result as bounded metrics.
   Never use database names, object keys, tenant identifiers, URLs, or error
   payloads as metric labels.
6. Append a sensitive audit event for manual recovery actions. Automated job
   telemetry stays in operational logs.

## Backup failure

`PostqronDatabaseBackupStale` pages the on-call responder when no verified
database backup has completed in 24 hours.

1. Acknowledge the alert and appoint an Incident Commander.
2. Preserve the last known-good backup and WAL chain; do not delete or overwrite
   recovery material while diagnosing.
3. Check scheduler status, storage availability, checksum result, expiry policy,
   and secret-manager access. Use redacted logs only.
4. Retry with the approved recovery identity. Never copy production data to a
   developer machine or a lower environment.
5. If the gap can exceed the 15-minute RPO, declare a recovery incident and
   notify Engineering and Security owners.
6. Record outcome, measured exposure, owner, and corrective action in the
   incident system.

## Restore procedure

1. Declare the incident start time; this starts RTO measurement. Assign Incident
   Commander, Recovery Operator, Security Owner, and application verifier.
2. Create an isolated recovery environment with outbound provider calls and
   transactional email blocked.
3. Select the newest consistent base backup and WAL point before the incident.
   Record immutable backup identifiers and checksums in the restricted incident
   record, not in application logs.
4. Restore PostgreSQL and replay WAL to the selected point in time. Restore
   objects from the matching encrypted snapshot/version.
5. Apply forward-only migrations and validate schema, referential integrity,
   durable job counts, object checksum samples, and audit continuity.
6. Apply all non-expired deletion tombstones before traffic is enabled. Re-delete
   restored personal data within 24 hours.
7. Rebuild derived Redis state from PostgreSQL. No durable job may be recovered
   only from Redis.
8. Start the API and one worker with provider side effects disabled. Run the
   idempotent simulated publication and prove that replay cannot duplicate it.
9. Security verifies secret rotation requirements and network controls. The
   Incident Commander approves reopening only after all checks pass.
10. Record achieved recovery point and service restoration time, deviations,
    approvals, and follow-up work. Retain the drill/incident report for 13
    months.

## Restore drills

- Monthly: restore the latest database backup in isolation and verify schema,
  integrity, counts, object checksum samples, and deletion tombstones.
- Quarterly: recover the minimum end-to-end service using only backups,
  repository, and secret manager; measure RPO/RTO and run a simulated
  idempotent job with external effects blocked.
- Annually, after every severe incident, and after material architecture
  changes: run a disaster-recovery tabletop.

A failed drill opens an owned, dated corrective ticket and does not count as a
successful test. The `PostqronRestoreTestOverdue` alert remains active until a
successful test updates the metric.
