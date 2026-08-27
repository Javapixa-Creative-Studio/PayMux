# syntax=docker/dockerfile:1

# ---- build ----------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies are copied first so the module cache survives source edits.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/paymux-worker ./apps/worker

# ---- runtime --------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/paymux-worker /paymux-worker

USER nonroot:nonroot

# Prometheus metrics only, and unauthenticated. Declared so a platform can see
# the port; do not give this one a domain.
EXPOSE 9090

# Exec form: the distroless runtime has no shell to expand a string.
HEALTHCHECK --interval=15s --timeout=5s --retries=5 \
  CMD ["/paymux-worker", "-healthcheck"]

ENTRYPOINT ["/paymux-worker"]
