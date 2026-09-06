#!/bin/bash
# Seed the deploy host's taxman database volume from a local SQLite file.
#
# Usage: TAXMAN_DEPLOY_SERVER=user@host ./scripts/bootstrap-db.sh [path/to/taxman.db] [--force]
#        (defaults to ./taxman.db)
#
# This copies REAL financial data from this machine to the deploy host
# over SSH only. It is never committed and never sent anywhere else. Run it
# once, before or instead of the first `docker compose up`; migrations
# run on container startup, so an older-schema seed is upgraded in place.
set -euo pipefail

SERVER="${TAXMAN_DEPLOY_SERVER:?set TAXMAN_DEPLOY_SERVER, e.g. user@192.168.1.10}"
DEPLOY_DIR="${TAXMAN_DEPLOY_DIR:-~/services/taxman}"
SRC="${1:-taxman.db}"
FORCE="${2:-}"

[ -f "$SRC" ] || { echo "no such db file: $SRC" >&2; exit 1; }
head -c 16 "$SRC" | grep -q "SQLite format 3" \
  || { echo "$SRC is not a SQLite database" >&2; exit 1; }

echo "Seeding $SERVER:$DEPLOY_DIR taxman volume from $SRC"
scp "$SRC" "$SERVER:/tmp/taxman-seed.db"

ssh "$SERVER" "set -euo pipefail; cd $DEPLOY_DIR && \
  if [ '$FORCE' != '--force' ] && \
     docker compose run --rm --no-deps -T --entrypoint sh taxman \
       -c 'test -s /data/taxman.db' 2>/dev/null; then \
    echo 'refusing: /data/taxman.db already exists on the volume (pass --force to overwrite)' >&2; \
    rm -f /tmp/taxman-seed.db; exit 1; \
  fi; \
  docker compose stop taxman 2>/dev/null || true; \
  docker compose run --rm --no-deps -T -v /tmp:/seed:ro --entrypoint sh taxman \
    -c 'cp /seed/taxman-seed.db /data/taxman.db && rm -f /data/taxman.db-wal /data/taxman.db-shm'; \
  rm -f /tmp/taxman-seed.db; \
  docker compose up -d --build; \
  sleep 2; \
  docker compose exec -T taxman taxman classify --list-unclassified --db /data/taxman.db || true"

echo "Done."
