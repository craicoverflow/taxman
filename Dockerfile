# syntax=docker/dockerfile:1

# ---- build -----------------------------------------------------------------
# modernc.org/sqlite is pure Go, so the binary builds with CGO disabled and
# needs no C toolchain or shared libraries at runtime.
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/taxman ./cmd/taxman

# ---- runtime -------------------------------------------------------------
FROM alpine:3.20

# ca-certificates: outbound HTTPS to the portfolio price feeds
#   (Yahoo / stooq / finnhub).
# tzdata: taxman resolves effective-dated FX and tax rates by calendar
#   date, so the container needs a real zoneinfo database.
RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -H -u 10001 taxman \
    && mkdir -p /data \
    && chown taxman:taxman /data

COPY --from=build /out/taxman /usr/local/bin/taxman

USER taxman
WORKDIR /data
VOLUME ["/data"]

EXPOSE 8080

# The SQLite database lives on the /data volume; migrations run on
# `serve` startup. Bind 0.0.0.0 so the port is reachable from the
# Docker network (the reverse proxy terminates outside the container).
ENTRYPOINT ["taxman"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8080", "--db", "/data/taxman.db"]
