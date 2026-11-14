# syntax=docker/dockerfile:1
# One builder compiles every binary; four final targets: web, scheduler,
# worker and operator (/migrate + /user). See docs/adr/0005.

# Builder runs on the build host and cross-compiles for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27.2 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    for c in web scheduler worker migrate user; do \
      GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/$c ./cmd/$c || exit 1; \
    done

FROM gcr.io/distroless/static-debian12:nonroot AS web
COPY --from=build /out/web /web
ENTRYPOINT ["/web"]

FROM gcr.io/distroless/static-debian12:nonroot AS scheduler
COPY --from=build /out/scheduler /scheduler
ENTRYPOINT ["/scheduler"]

FROM gcr.io/distroless/static-debian12:nonroot AS worker
COPY --from=build /out/worker /worker
ENTRYPOINT ["/worker"]

# No entrypoint: compose passes `command` (/migrate or /user ...).
FROM gcr.io/distroless/static-debian12:nonroot AS operator
COPY --from=build /out/migrate /migrate
COPY --from=build /out/user /user
