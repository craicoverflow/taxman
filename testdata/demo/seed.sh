#!/usr/bin/env bash
#
# Build the demo database the README screenshots come from.
#
# Everything this seeds is fabricated: invented instruments, ISINs,
# amounts, interest credits and prices. No real financial data is in
# this repo (SPEC.md §6) and none should ever be added here.
#
# Usage: testdata/demo/seed.sh [db-path]   (default: tmp/demo.db)
#
# Interest credits go in over HTTP because manual credits have no CLI
# path — they're a web-only entry point — so the script briefly runs
# the server to post them. Prices are written straight to the quote
# cache: the demo tickers are invented and no price provider knows
# them, so a live lookup would return nothing.
set -euo pipefail

cd "$(dirname "$0")/../.."

DB="${1:-tmp/demo.db}"
PORT="${DEMO_PORT:-8099}"
BIN=./bin/taxman

command -v sqlite3 >/dev/null || { echo "demo seed: sqlite3 is required" >&2; exit 1; }
[ -x "$BIN" ] || { echo "demo seed: $BIN not built — run 'make build'" >&2; exit 1; }

mkdir -p "$(dirname "$DB")"
rm -f "$DB"

"$BIN" import testdata/demo/demo-degiro.csv --db "$DB"

# The two savings accounts are deliberately left unclassified: DIRT is
# orthogonal to the CGT/exit-tax split and needs no classification.
"$BIN" classify --set IE00DEMO0001 EXIT_TAX_FUND --db "$DB"
"$BIN" classify --set IE00DEMO0002 EXIT_TAX_FUND --db "$DB"
"$BIN" classify --set US00DEMO0003 CGT_ASSET --db "$DB"

# Refuse to share the port: something else already listening would
# answer the readiness probe, and the credits below would silently go
# into that instance's database instead of this one.
if curl -sf -o /dev/null --max-time 2 "http://localhost:$PORT/" 2>/dev/null; then
	echo "demo seed: something is already listening on port $PORT — stop it or set DEMO_PORT" >&2
	exit 1
fi

"$BIN" serve --port "$PORT" --db "$DB" >/dev/null 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true' EXIT
ready=""
for _ in $(seq 1 50); do
	if curl -sf -o /dev/null "http://localhost:$PORT/"; then ready=1; break; fi
	sleep 0.2
done
[ -n "$ready" ] || { echo "demo seed: server did not come up on port $PORT" >&2; exit 1; }

post_interest() {
	curl -sf -o /dev/null -X POST "http://localhost:$PORT/interest" \
		--data-urlencode "source=$1" \
		--data-urlencode "date=$2" \
		--data-urlencode "amount=$3"
}
post_interest n26 2024-06-30 142.85
post_interest n26 2024-12-31 168.40
post_interest traderepublic 2024-09-30 96.20

kill "$server" 2>/dev/null || true
wait "$server" 2>/dev/null || true
trap - EXIT

now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sqlite3 "$DB" <<SQL
INSERT OR REPLACE INTO instrument_tickers (instrument, symbol, created_at) VALUES
 ('IE00DEMO0001','DEMOW','$now'),
 ('IE00DEMO0002','DEMOE','$now'),
 ('US00DEMO0003','WDGT','$now');
INSERT OR REPLACE INTO price_quotes (symbol, price, currency, fetched_at) VALUES
 ('DEMOW','95.00','EUR','$now'),
 ('DEMOE','55.00','EUR','$now'),
 ('WDGT','48.00','EUR','$now');
SQL

echo "demo database ready at $DB — 'taxman serve --db $DB' (tax year 2024)"
