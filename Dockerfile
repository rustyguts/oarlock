# oarlock all-in-one image: the Go binary serves the API, the workers, and
# the embedded web UI. OARLOCK_MODE picks the role at runtime:
#   all    — UI + API + workers in one process (default; single-container)
#   api    — UI + API only (HA: scale separately from workers)
#   worker — workers only, /healthz for probes

# Build stages run natively on the build host ($BUILDPLATFORM) and
# cross-compile for the target — multi-arch builds never pay the QEMU tax on
# the heavy web/Go stages, only the trivial runtime stage runs emulated.

# ---- web UI (static SPA; arch-independent output) ----
FROM --platform=$BUILDPLATFORM oven/bun:1-alpine AS web
WORKDIR /web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ .
RUN bun run build

# ---- Go binary embedding the UI (CGO off → pure cross-compile) ----
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY engine/go.mod engine/go.sum ./
RUN go mod download
COPY engine/ .
COPY --from=web /web/build ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/oarlock ./cmd/api

# ---- runtime ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 oarlock
USER oarlock
COPY --from=build /out/oarlock /usr/local/bin/oarlock
EXPOSE 9000
ENTRYPOINT ["oarlock"]
