#!/bin/bash
# Sync the latest committed changes to the deploy host and rebuild the
# taxman container in place.
#
# Usage: TAXMAN_DEPLOY_SERVER=user@host ./scripts/deploy.sh
#
# Assumes the repo is already cloned at $DEPLOY_DIR on the box and that
# .env.prod (if used) is present there — it is gitignored and never synced.
set -euo pipefail

SERVER="${TAXMAN_DEPLOY_SERVER:?set TAXMAN_DEPLOY_SERVER, e.g. user@192.168.1.10}"
DEPLOY_DIR="${TAXMAN_DEPLOY_DIR:-~/services/taxman}"

echo "Deploying taxman to $SERVER:$DEPLOY_DIR ..."

ssh "$SERVER" "set -euo pipefail; \
  cd $DEPLOY_DIR && \
  git pull --ff-only && \
  docker compose up -d --build && \
  docker image prune -f && \
  docker compose ps"

echo "Done."
