# syntax=docker/dockerfile:1.7

# Static build stage. CGO is disabled so the binary can run in a distroless
# image with no libc, shell or package manager.
FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/molisync-server ./cmd/server

# The collector is a separate entrypoint in the same module. It is only copied
# into the collector image, not into the server image, so each container has a
# single responsibility.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/molisync-collector ./cmd/collector

# Pre-create the persistent data directory with the runtime UID so a named
# volume initialised from this image is writable by the nonroot user.
RUN mkdir -p /out/data /out/collector-data && chown -R 65532:65532 /out/data /out/collector-data

# Runtime stage: distroless, non-root, no shell.
FROM gcr.io/distroless/static-debian12:nonroot AS server

COPY --from=build /out/molisync-server /usr/local/bin/molisync-server
COPY --from=build --chown=65532:65532 /out/data /var/lib/molisync

# The service must accept connections from the reverse proxy container, so the
# in-container default binds all interfaces. Nothing is published to the host
# by the image itself.
ENV MOLISYNC_SERVER_LISTEN=0.0.0.0:9520
ENV MOLISYNC_STORAGE_DATA_DIR=/var/lib/molisync

EXPOSE 9520

USER nonroot:nonroot

# Uses the server binary as the probe because the image has no curl. If you
# override MOLISYNC_SERVER_LISTEN, override this health check too.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/molisync-server", "-healthcheck-url", "http://127.0.0.1:9520/healthz"]

ENTRYPOINT ["/usr/local/bin/molisync-server"]

# Collector image. Deploy it on a public host to aggregate the anonymous
# reports; it never stores device identifiers.
FROM gcr.io/distroless/static-debian12:nonroot AS collector

COPY --from=build /out/molisync-collector /usr/local/bin/molisync-collector
COPY --from=build --chown=65532:65532 /out/collector-data /var/lib/molisync-collector

ENV MOLISYNC_COLLECTOR_LISTEN=0.0.0.0:9530
ENV MOLISYNC_COLLECTOR_DATA_DIR=/var/lib/molisync-collector

EXPOSE 9530

USER nonroot:nonroot

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/molisync-collector", "-healthcheck-url", "http://127.0.0.1:9530/healthz"]

ENTRYPOINT ["/usr/local/bin/molisync-collector"]
