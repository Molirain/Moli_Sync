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

# Runtime stage: distroless, non-root, no shell.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/molisync-server /usr/local/bin/molisync-server

# The service must accept connections from the reverse proxy container, so the
# in-container default binds all interfaces. Nothing is published to the host
# by the image itself.
ENV MOLISYNC_SERVER_LISTEN=0.0.0.0:9520

EXPOSE 9520

USER nonroot:nonroot

# Uses the server binary as the probe because the image has no curl. If you
# override MOLISYNC_SERVER_LISTEN, override this health check too.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/molisync-server", "-healthcheck-url", "http://127.0.0.1:9520/healthz"]

ENTRYPOINT ["/usr/local/bin/molisync-server"]
