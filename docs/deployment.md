# Production deployment

`.github/workflows/deploy.yml` restores deployment to the existing SIR host:
`admin@44.205.131.239`, `/opt/sir-server`, systemd service `sir-tracker`, Java 21,
and the existing `conf/sir-prd.xml`. It is **manual**, not triggered by pushes.
Only runs from `master` may deploy; publishing the workflow alone does not restart production.

After merging, open **Actions → Deploy Production (SIR) → Run workflow → master**.
The `production` GitHub environment can enforce approval before the job starts.

## GitHub configuration

The following repository secrets already exist (presence verified, contents not read):

- `DEPLOY_SSH_PRIVATE_KEY`: SSH key authorized for the deployment user.
- `DEPLOY_KNOWN_HOSTS`: trusted server host-key entries. Strict verification is enabled.

Existing repository variables are reused:

| Variable | Current target |
| --- | --- |
| `DEPLOY_HOST` | `44.205.131.239` |
| `DEPLOY_USER` | `admin` |
| `DEPLOY_PORT` | `22` |
| `DEPLOY_PATH` | `/opt/sir-server` |
| `DEPLOY_SERVICE_NAME` | `sir-tracker` |
| `DEPLOY_CONFIG_FILE` | `conf/sir-prd.xml` |

The SSH user requires passwordless sudo. The host requires Bash, rsync, curl,
Python 3, flock, sha256sum and Java 21. The workflow does not install or change Java.
The server's `ExecStart` already uses its Java 21 installation.

## Deployment behavior

1. Build and test with Java 21, including Checkstyle. Simulate deployment success
   and failed-health rollback with mocked system commands.
2. Upload the JAR, libraries, schema files and templates to a unique `/tmp` directory.
   Verify every uploaded file against SHA-256 checksums. Library checksums let rsync
   reuse unchanged dependencies from the live library directory.
3. Lock deployment and check existing API health, required paths and 2 GiB free space.
4. Snapshot the current server files under `.deploy-backups/`, then stop `sir-tracker`,
   promote the release and start it again. This causes a brief tracking interruption.
5. Wait for `/api/server` and the native batch route through Nginx, then check again
   after ten seconds. The batch probe submits an empty batch and expects HTTP 400
   with JSON containing `accepted: 0`; it writes no positions.
6. On installation/startup/health failure, restore the old server files, restart the
   previous version and report whether its API health recovered. The action still fails.
7. Clean up this run's staging directory and runner SSH credentials. Successful
   deployments record their Git SHA in `.deployed-revision`.

The existing Nginx route `/api/position-mobile/batch` must point to OsmAnd on port 5055
as configured in this chat. Legacy `/position-mobile/` continues to work.

Configuration, frontend, local data and logs are preserved. No TrackHub changes are made.
The workflow uploads matching schema files because the running JAR can depend on them.
Traccar may apply database migrations during startup; restoring server files does **not**
undo database migrations. Backups are retained for recovery and should be periodically
reviewed/archived; deployment stops before replacing live files if disk space is too low.

## Validation boundary

Workflow YAML and shell syntax, plus the simulated successful deployment and rollback,
are checked locally. The existing server layout, Java runtime and `/api/server` health
were inspected read-only. A real deployment has not been run, and existing GitHub secret
contents have not been tested. Check the first production run for SSH/host-key validity
and startup compatibility before treating the new batch endpoint as live.
