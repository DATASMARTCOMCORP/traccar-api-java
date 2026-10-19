#!/usr/bin/env bash
# Run on the production host via sudo after the complete release is uploaded.
set -euo pipefail

stage=${1:?release directory required}
app=${2:?application directory required}
service=${3:?service name required}
config=${4:?relative config path required}
revision=${5:?revision required}

[[ "$stage" == /tmp/sir-tracker-deploy-* && "$app" == /* && "$app" != / ]]
[[ "$service" =~ ^[a-zA-Z0-9_-]+$ && "$revision" =~ ^[a-f0-9]{40}$ ]]
[[ "$config" != /* && "$config" != *..* ]]
for item in tracker-server.jar lib schema templates; do
    test -e "$stage/$item"
    test -e "$app/$item"
done
test -f "$app/$config"
test -n "$(find "$stage/lib" -name '*.jar' -print -quit)"
# Never interleave two deployments, including manually invoked deployments.
exec 9>"$app/.deploy.lock"
flock -n 9
systemctl is-active --quiet "$service"
# Require working baseline before stopping the service.
curl -fsS --max-time 10 -o /dev/null http://127.0.0.1:8082/api/server
test "$(df -Pk "$app" | awk 'NR==2 {print $4}')" -ge 2097152

backup="$app/.deploy-backups/$(date -u +%Y%m%dT%H%M%SZ)-${revision:0:12}"
mkdir -p "$backup"
for item in tracker-server.jar lib schema templates; do
    # Hard links remain an immutable snapshot because promotion uses rename.
    cp -al "$app/$item" "$backup/$item"
done
if [[ -f "$app/.deployed-revision" ]]; then
    cp -p "$app/.deployed-revision" "$backup/revision"
fi

promoted=()
rollback() {
    local rc=$?
    trap - EXIT HUP INT TERM
    if (( rc != 0 )); then
        echo 'Deployment failed; restoring the previous server files.' >&2
        if systemctl stop "$service"; then
            for item in "${promoted[@]}"; do
                rm -rf -- "$app/.incoming-${revision:0:12}-$item"
                rm -rf -- "$app/$item"
                cp -a "$backup/$item" "$app/$item"
            done
            if [[ -f "$backup/revision" ]]; then
                cp -p "$backup/revision" "$app/.deployed-revision"
            fi
            systemctl start "$service" || echo 'ERROR: previous service failed to start.' >&2
            restored=false
            for ((attempt=0; attempt<30; attempt++)); do
                if systemctl is-active --quiet "$service" \
                        && curl -fsS --max-time 5 -o /dev/null http://127.0.0.1:8082/api/server; then
                    restored=true
                    break
                fi
                sleep 5
            done
            echo "Previous service health restored: $restored" >&2
        else
            echo 'ERROR: could not stop service; files were not overwritten during rollback.' >&2
        fi
        echo "Backup retained at $backup" >&2
    fi
    exit "$rc"
}
trap rollback EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

systemctl stop "$service"
for item in tracker-server.jar lib schema templates; do
    # Copy while stopped, then rename on the application filesystem. Register
    # each item first so a failed copy/rename also restores that item.
    promoted+=("$item")
    incoming="$app/.incoming-${revision:0:12}-$item"
    test ! -e "$incoming"
    cp -a "$stage/$item" "$incoming"
    rm -rf -- "$app/$item"
    mv "$incoming" "$app/$item"
done
systemctl start "$service"

healthy() {
    systemctl is-active --quiet "$service" || return 1
    curl -fsS --max-time 5 -o /dev/null http://127.0.0.1:8082/api/server || return 1
    # An empty batch must produce the native JSON validation response, never a write.
    local response
    response=$(curl -sS --max-time 5 -w '\n%{http_code}' \
        -H 'Host: traccar.datasmartcom.com.br' -H 'Content-Type: application/json' \
        --data '{"positions":[]}' http://127.0.0.1/api/position-mobile/batch) || return 1
    [[ "${response##*$'\n'}" == 400 ]] || return 1
    python3 -c 'import json,sys; r=json.load(sys.stdin); assert r["accepted"] == 0 and r.get("error")' \
        <<< "${response%$'\n'*}"
}

ready=false
for ((attempt=0; attempt<30; attempt++)); do
    if healthy; then
        ready=true
        break
    fi
    sleep 5
done
[[ "$ready" == true ]]
# Catch processes that briefly come up and then crash.
sleep 10
healthy
printf '%s\n' "$revision" > "$app/.deployed-revision"
trap - EXIT HUP INT TERM
echo "Deployment of $revision verified. Backup: $backup"
# Configuration, frontend, local data, logs and database are never copied or deleted.
