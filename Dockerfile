# syntax=docker/dockerfile:1

# Caravan's container image (SPEC §2.1): one process, one /config volume, one
# /data volume. See docs/docker.md for the deployment contract.

# ---------------------------------------------------------------------------
# Stage 1 — the SPA.
#
# web/dist is committed (go:embed needs *something* there for `go build` to
# work at all), but nothing in CI proves the committed copy matches web/src.
# The image therefore rebuilds it and .dockerignore keeps the committed copy
# out of the build context entirely, so an image is a pure function of the
# source tree rather than of whoever last remembered to run `npm run build`.
# ---------------------------------------------------------------------------
FROM node:22-alpine AS web

WORKDIR /src/web

# Manifests first: dependencies only re-resolve when they actually change.
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2 — the binary.
#
# CGO_ENABLED=0 because sqlite is modernc.org/sqlite, pure Go (SPEC §4): the
# result is a static binary that runs on a base image with no libc contract.
#
# Tracks the 1.26 line rather than a patch, to keep picking up Go's security
# fixes; go.mod names the floor and GOTOOLCHAIN fetches it if the tag ever lags.
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS build

# Set by buildx; empty under a plain `docker build`, where an empty GOOS/GOARCH
# correctly means "this builder's own target".
ARG TARGETOS
ARG TARGETARCH
# Release builds pass the tag: --build-arg VERSION=v1.2.3.
ARG VERSION=docker

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# After the source copy, so the freshly built SPA wins over anything that
# slipped through the build context.
COPY --from=web /src/web/dist ./web/dist

ENV CGO_ENABLED=0
RUN GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/caravan ./cmd/caravan

# ---------------------------------------------------------------------------
# Stage 3 — the image.
#
# Alpine rather than distroless: ffmpeg is an optional runtime dependency
# (SPEC §8) that users add by deriving from this image, and that is a one-line
# `apk add` only if there is an apk. See docs/docker.md.
# ---------------------------------------------------------------------------
FROM alpine:3.22

# ca-certificates: TMDB and every indexer are HTTPS.
# tzdata: so the TZ environment variable actually resolves — Go has no zone
# database of its own unless it is built with the timetzdata tag.
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 1000 caravan \
    && adduser -u 1000 -G caravan -h /config -D caravan \
    && mkdir -p /config /data \
    && chown caravan:caravan /config /data

COPY --from=build /out/caravan /usr/local/bin/caravan

# The container's whole configuration (SPEC §2.1). These are environment
# overrides rather than a baked config file so `docker run` needs no file, and
# so a bind-mounted /config/caravan.yaml can still set everything else.
#
# storage_root only seeds the settings table on first run: a root re-pointed
# from the UI later stays re-pointed across restarts.
ENV CARAVAN_CONFIG_DIR=/config \
    CARAVAN_STORAGE_ROOT=/data \
    CARAVAN_LISTEN=0.0.0.0:8677 \
    HOME=/config

# One /data volume, deliberately. Library and downloads both live under the
# storage root, so a single mount is what makes hardlink imports and atomic
# moves work by construction (SPEC §2.1). Splitting them is the classic *arr
# trap; docs/docker.md says so at length.
VOLUME ["/config", "/data"]

EXPOSE 8677

USER caravan

# The SPA index, not /api/v1/system/status: status sits inside the auth gate
# and starts answering 401 the moment a password is set, which would flip a
# healthy container to unhealthy for doing exactly what SPEC §11 asks. "/" is
# outside the gate in every configuration and still proves the process is
# listening and its handler tree is built.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8677/ || exit 1

ENTRYPOINT ["/usr/local/bin/caravan"]
CMD ["serve", "-config", "/config/caravan.yaml"]
