#!/usr/bin/env bash
# Runs the integration tests against a real MongoDB replica set.
# Uses $MSTASH_TEST_MONGO_URI when set; otherwise starts a throwaway
# single-node replica set in Docker and removes it afterwards.
set -euo pipefail

if [[ -z "${MSTASH_TEST_MONGO_URI:-}" ]]; then
  name="m-stash-itest-$$"
  port="${MSTASH_TEST_MONGO_PORT:-27099}"
  docker run -d --rm --name "$name" -p "127.0.0.1:${port}:27017" mongo:8 --replSet rs0 --bind_ip_all >/dev/null
  trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
  for _ in $(seq 1 60); do
    docker exec "$name" mongosh --quiet --eval "db.adminCommand('ping').ok" >/dev/null 2>&1 && break
    sleep 1
  done
  docker exec "$name" mongosh --quiet --eval "rs.initiate({_id: 'rs0', members: [{_id: 0, host: '127.0.0.1:27017'}]})" >/dev/null
  for _ in $(seq 1 60); do
    [[ "$(docker exec "$name" mongosh --quiet --eval 'db.hello().isWritablePrimary')" == "true" ]] && break
    sleep 1
  done
  export MSTASH_TEST_MONGO_URI="mongodb://127.0.0.1:${port}/?replicaSet=rs0&directConnection=true"
fi

go test -race -count=1 -run Integration "$@" ./...
