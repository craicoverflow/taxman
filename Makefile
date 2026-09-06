.PHONY: build test lint fuzz bdd regen-features docs check-features check-docs ci dev demo docker-build deploy

# Load a local .env (gitignored) if present and export its vars to
# every recipe, so `make dev` picks up e.g. TAXMAN_FINNHUB_TOKEN for
# the portfolio page's price feed. Copy .env.example to .env to set it.
# CI has no .env, so this is a no-op there.
-include .env
export

build:
	go build -o bin/taxman ./cmd/taxman

test:
	go test -race ./...

lint:
	golangci-lint run

fuzz:
	go test -fuzz=Fuzz -fuzztime=60s ./internal/engine/...

# bdd runs the executable, accountant-facing specification in features/
# (Gherkin via godog). Also covered by `make test`; this is the
# human-facing entry point with readable output.
bdd:
	go test ./features/... -v

# regen-features re-runs each scenario and rewrites the pinned figures
# in features/*.feature from the engine's current output. Run this after
# any change to internal/engine or internal/taxrules — CI fails if the
# feature files are out of date (the drift gate below in `ci`).
regen-features:
	REGEN_FEATURES=1 go test ./features/... -run TestFeatures -count=1

# docs renders docs/maths.md plus the feature files into a single,
# self-contained docs/maths.html for sharing with an accountant. Uses
# the Go standard library only; opens offline (Markdown renders via a
# CDN script, degrading to readable raw text).
docs:
	go run ./cmd/gendocs -out docs/maths.html

# demo builds a throwaway database from the fabricated dataset in
# testdata/demo and serves it on :8099. This is the dataset every
# README screenshot comes from, so the screenshots are reproducible.
# Nothing in it is real — see the header of testdata/demo/seed.sh.
DEMO_DB ?= tmp/demo.db
demo: build
	testdata/demo/seed.sh $(DEMO_DB)
	./bin/taxman serve --port 8099 --db $(DEMO_DB)

ci: build test lint check-features check-docs

# check-features is the drift gate: regenerating the pinned figures must
# be a no-op on a clean tree. If it isn't, an engine/taxrules change
# moved a number without refreshing the worked examples.
#
# This re-runs scenarios `make test` has already run, but the two passes
# check different things: `make test` asserts the pinned figures under
# -race, while regen mode skips the assertions and rewrites them. The
# suite is well under a second, so the overlap is deliberate, not waste.
#
# The comparison is against a snapshot taken just before the regen, not
# against git: the gate must answer "did regenerating move a figure?",
# which is independent of whether the tree happens to be dirty. A
# git-based diff would also flag the .go files that share this directory
# and that regen never touches.
check-features:
	@snap=$$(mktemp -d); trap 'rm -rf "$$snap"' EXIT; \
	cp features/*.feature "$$snap/"; \
	$(MAKE) --no-print-directory regen-features || exit 1; \
	stale=""; \
	for f in features/*.feature; do \
		diff -u "$$snap/$$(basename "$$f")" "$$f" && continue; \
		stale="$$stale $$f"; \
	done; \
	if [ -n "$$stale" ]; then \
		echo; \
		echo "worked examples were stale and have been regenerated in place —"; \
		echo "review the diff above and commit:$$stale"; \
		exit 1; \
	fi

# check-docs is the same drift gate for the accountant-facing HTML page.
# gendocs output is a pure function of docs/maths.md and features/ (the
# footer carries a source fingerprint, not a generation date), so
# regenerating on a clean tree must be a no-op.
check-docs:
	@snap=$$(mktemp -d); trap 'rm -rf "$$snap"' EXIT; \
	cp docs/maths.html "$$snap/"; \
	$(MAKE) --no-print-directory docs || exit 1; \
	if ! cmp -s "$$snap/maths.html" docs/maths.html; then \
		echo; \
		echo "docs/maths.html was stale and has been regenerated in place — commit it."; \
		exit 1; \
	fi

# dev runs `taxman serve` under air, rebuilding and restarting on
# every .go change. Config: .air.toml. Install air with:
#   go install github.com/air-verse/air@latest
dev:
	@command -v air >/dev/null 2>&1 || { \
		echo "air not found. Install it with:"; \
		echo "  go install github.com/air-verse/air@latest"; \
		exit 1; \
	}
	air

# docker-build builds the deployment image locally (same Dockerfile the
# homelab uses). Handy for checking a change compiles in the container
# before pushing.
docker-build:
	docker build -t taxman:local .

# deploy syncs committed changes to the homelab and rebuilds the
# container in place (git pull + docker compose up -d --build). Override
# the target with TAXMAN_DEPLOY_SERVER / TAXMAN_DEPLOY_DIR.
deploy:
	./scripts/deploy.sh
