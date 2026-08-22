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
      -o /out/paymux-api ./apps/api

# ---- runtime --------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/paymux-api /paymux-api

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/paymux-api"]
