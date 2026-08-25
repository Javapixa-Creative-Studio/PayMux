# syntax=docker/dockerfile:1

# The demo storefront. One image, run twice with different environment, which
# is the whole point: two independent shops, one merchant account.

# ---- build ----------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags "-s -w" \
      -o /out/demo-shop ./examples/demo-shop

# ---- runtime --------------------------------------------------------------
# busybox rather than distroless: the healthcheck needs a shell to reach the
# port, and a demo that reports itself unhealthy is a demo people distrust.
FROM busybox:stable-musl

COPY --from=build /out/demo-shop /demo-shop

USER 65534:65534

ENTRYPOINT ["/demo-shop"]
