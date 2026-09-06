# Deploying taxman with Docker

taxman ships as a single static Go binary with an embedded SQLite
database and a no-build-step HTML UI. The container wraps exactly that.

## Image

`Dockerfile` is a two-stage build:

1. `golang:1.25-alpine` compiles `./cmd/taxman` with `CGO_ENABLED=0`
   (`modernc.org/sqlite` is pure Go — no C toolchain, no libsqlite).
2. `alpine:3.20` runtime with `ca-certificates` (outbound HTTPS for the
   `/portfolio` price feeds), `tzdata` (effective-dated FX/tax-rate
   lookups resolve by calendar date), and `wget` (healthcheck). Runs as
   an unprivileged user; the database lives on the `/data` volume.

The container command binds `0.0.0.0:8080` inside the container —
`taxman serve` otherwise binds loopback only (`--host` default
`127.0.0.1`), which is correct for a bare local run but unreachable
across a Docker network. Host exposure is controlled at the `ports:`
layer (see below), not by this bind.

## Homelab deployment (`~/services`)

```sh
git clone <repo> ~/services/taxman
cd ~/services/taxman
cp .env.prod.example .env.prod        # optional — only TAXMAN_FINNHUB_TOKEN
docker compose up -d --build
```

### Access

taxman has no authentication of its own, so it is **not** on
`npm_network`, has no public hostname, and is not behind
Cloudflare/NPM/Authelia. The compose file publishes the port on the
host as `8088:8080`, reachable at:

- `http://192.168.1.10:8088/` over the LAN
- the same address from the tailnet (the box advertises the
  `192.168.1.0/24` route; accept it on the peer with
  `tailscale up --accept-routes`), or via the box's own tailnet IP

Keep it off the public internet — there is no ingress path to it from
outside the LAN/tailnet, and it should stay that way given there is no
login.

For an HTTPS hostname instead of `IP:8088`, run `tailscale serve --bg
--https=443 http://127.0.0.1:8088` on the box: it fronts taxman at
`https://<node>.<tailnet>.ts.net` with a real cert, tailnet-only (no
Funnel). Optional; lives outside compose.

### Seeding the database from an existing local file

To bring up the deployment with your existing ledger instead of an
empty database, run this from the dev checkout **before** (or instead
of) the first `docker compose up`:

```sh
./scripts/bootstrap-db.sh ./taxman.db
```

It `scp`s the file to the box and copies it into the `taxman_data`
volume via a throwaway container (never through git — the DB holds real
financial data). It refuses if the volume already has a non-empty
`taxman.db`; pass `--force` to overwrite. Migrations run on the next
`serve` startup, so a seed from an older schema is upgraded in place.

## Data and backups

All state is the SQLite database on the `taxman_data` named volume
(`/data/taxman.db` plus its WAL). Nothing else is stateful. Schema
migrations run automatically on `serve` startup.

Back it up with a consistent copy:

```sh
docker compose exec taxman sh -c \
  'taxman migrate --db /data/taxman.db >/dev/null; \
   sqlite3 /data/taxman.db ".backup /data/taxman-backup.db"' 2>/dev/null || \
docker run --rm -v taxman_taxman_data:/data -v "$PWD":/out alpine \
  sh -c 'apk add --no-cache sqlite >/dev/null && \
         sqlite3 /data/taxman.db ".backup /out/taxman-backup.db"'
```

(`sqlite3` is not in the runtime image; the second form does the backup
from a throwaway container.) This volume is **not** covered by the
`~/services` DR backup script yet — add it there if taxman holds data
you care about.

## Running CLI subcommands

Every `taxman` subcommand takes `--db`; point it at the volume path.

```sh
# Against the running container (shares the live database):
docker compose exec taxman taxman classify --list-unclassified
docker compose exec taxman taxman classify --set IE00B4L5Y983 EXIT_TAX_FUND
docker compose exec taxman taxman report --year 2024 --db /data/taxman.db

# Import a broker export — drop the file in ./imports on the host first:
docker compose exec taxman taxman import /imports/degiro-2024.csv --db /data/taxman.db
```

`import`, `classify`, `report`, `backfill` and `migrate` all default
`--db` to `taxman.db` in the working directory, which is `/data` in the
image, so `--db` is usually optional inside the container.

## Updating

From a dev machine that can SSH to the box:

```sh
make deploy          # → ./scripts/deploy.sh
```

That does `git pull --ff-only && docker compose up -d --build &&
docker image prune -f` over SSH against `~/services/taxman`. Override
the target with `TAXMAN_DEPLOY_SERVER` / `TAXMAN_DEPLOY_DIR`. `.env.prod`
lives only on the box (gitignored) and is never synced.

Or by hand on the box:

```sh
cd ~/services/taxman && git pull && docker compose up -d --build
```

The database migrates forward on startup. There is no down-migration
path in normal operation — snapshot the volume first if a release note
calls for it.
