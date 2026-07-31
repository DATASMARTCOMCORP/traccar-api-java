# [Traccar](https://www.traccar.org)

## Overview

Traccar is an open source GPS tracking system. This repository contains Java-based back-end service. It supports more than 200 GPS protocols and more than 2000 models of GPS tracking devices. Traccar can be used with any major SQL database system. It also provides easy to use [REST API](https://www.traccar.org/traccar-api/).

Other parts of Traccar solution include:

- [Traccar web app](https://github.com/traccar/traccar-web)
- [Traccar Manager app](https://github.com/traccar/traccar-manager)

There is also a set of mobile apps that you can use for tracking mobile devices:

- [Traccar Client app](https://github.com/traccar/traccar-client)

## Features

Some of the available features include:

- Real-time GPS tracking
- Driver behaviour monitoring
- Detailed and summary reports
- Geofencing functionality
- Alarms and notifications
- Account and device management
- Email and SMS support

## Build

Please read [build from source documentation](https://www.traccar.org/build/) on the official website.

## Deployment (DataSmartCom / SIR production)

Pushes to `master` (or a manual **Run workflow**) trigger
[`.github/workflows/deploy.yml`](.github/workflows/deploy.yml), which builds with Java 21
(`./gradlew clean assemble`) and deploys `target/tracker-server.jar` **together with the whole
`target/lib` directory** — the JAR is not self-contained, it resolves dependencies through its
manifest `Class-Path`.

On the server the workflow backs up the current JAR and `lib/`, stops `sir-tracker.service`,
installs the new files, restarts the service and validates it. If startup fails, or fatal errors
(`UnsupportedClassVersionError`, `NoClassDefFoundError`, `LiquibaseException`, `SQLException`,
`Address already in use`) appear in the logs, it restores the previous JAR and `lib/`, restarts the
service and fails the run. `conf/` (including `conf/sir-prd.xml`) and `log/` are never written or
deleted, only two application backups are retained, and temporary `/tmp` release directories are
cleaned up.

### Required GitHub Actions Secrets

| Secret | Value |
| --- | --- |
| `DEPLOY_SSH_PRIVATE_KEY` | Full contents of `~/.ssh/DataSmartCom.pem`, including the `BEGIN`/`END` lines |
| `DEPLOY_KNOWN_HOSTS` | Output of `ssh-keyscan -H 44.205.131.239` (host-key verification stays enabled) |

### Required GitHub Actions Variables

| Variable | Value |
| --- | --- |
| `DEPLOY_HOST` | `44.205.131.239` |
| `DEPLOY_USER` | `admin` |
| `DEPLOY_PORT` | `22` |
| `DEPLOY_PATH` | `/opt/sir-server` |
| `DEPLOY_SERVICE_NAME` | `sir-tracker` |
| `DEPLOY_CONFIG_FILE` | `conf/sir-prd.xml` |

### Optional GitHub Actions Variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `DEPLOY_JAVA_BIN` | `/home/admin/.sdkman/candidates/java/current/bin/java` | Java binary used by the service; must report major version 21 |
| `DEPLOY_HEALTH_PORT` | `web.port` from `conf/sir-prd.xml`, else `8082` | TCP port that must be listening after startup |
| `DEPLOY_HEALTH_COMMAND` | *(empty)* | Extra server-local health check, e.g. `curl -fsS -o /dev/null http://127.0.0.1:8082/api/server`. Empty by default: no public/unauthenticated endpoint is called |

No database credentials are used by the workflow and it never executes SQL. Database and Liquibase
problems (such as a missing `public.tc_device_device` table) are only surfaced through the service
logs the workflow prints; schema repairs must be done manually.

## Team

- Anton Tananaev ([anton@traccar.org](mailto:anton@traccar.org))
- Andrey Kunitsyn ([andrey@traccar.org](mailto:andrey@traccar.org))

## License

    Apache License, Version 2.0

    Licensed under the Apache License, Version 2.0 (the "License");
    you may not use this file except in compliance with the License.
    You may obtain a copy of the License at

        http://www.apache.org/licenses/LICENSE-2.0

    Unless required by applicable law or agreed to in writing, software
    distributed under the License is distributed on an "AS IS" BASIS,
    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
    See the License for the specific language governing permissions and
    limitations under the License.
