# MoliSync

**Secure, self-hosted cross-device file transfer and sync — built around mTLS identity and streaming I/O.**

[简体中文](README.zh-CN.md) · English

MoliSync is an open-source project for moving files and synchronized data between devices you control. The repository currently contains a Go control-plane service, a Windows .NET client, and an Android client. The components are under active development; see [Implementation status](#implementation-status) for what is and is not wired end to end.

## Repository layout

| Path | Component |
| --- | --- |
| [`server/`](server/) | Go control-plane service: trusted reverse-proxy authentication, device registry, WebSocket presence and HTTP API. Ships as a container image. |
| [`client-windows/`](client-windows/) | C# / .NET 8 Windows client: Windows certificate-store access, control-plane WebSocket, clipboard/placeholder handling, Range download and connection racing. |
| [`client-android/`](client-android/) | Kotlin / Jetpack Compose Android client: KeyChain mTLS, system share intents, streaming sender and foreground data-sync service. |
| [`proto/sync.proto`](proto/sync.proto) | Versioned control-plane and data-plane protocol definition. |
| [`docker-compose.example.yml`](docker-compose.example.yml) | Hardened Compose template for the server container. |
| [`.github/workflows/`](.github/workflows/) | Android build/release and server container image pipelines. |
| [Architecture whitepaper](MoliSync%20分布式跨端同步与点对点流式互传系统技术架构白皮书.md) | Original architecture and deployment design notes (Chinese). |

## Architecture

```text
 Windows client ─┐                       ┌─ Android client
                 ├── HTTPS / WebSocket ──┤
                 │   mTLS at proxy       │
                 └──────┬────────────────┘
                        │ authenticated identity headers
                 Trusted reverse proxy
                        │ private Docker network / loopback
                 Go control plane
                 device presence + endpoints

          Planned data plane: direct P2P, then encrypted relay
```

- **Control plane:** clients authenticate to a reverse proxy using client certificates. The proxy validates mTLS and forwards the verification result, certificate fingerprint, and subject to the Go service.
- **Device identity:** by default, the server uses the lowercase SHA-256 fingerprint of the client certificate as the stable `DeviceID`.
- **Proxy trust boundary:** the server accepts identity headers only from configured trusted proxy CIDRs. The proxy must remove client-supplied copies of those headers before setting its own values.
- **Data plane:** the protocol defines framed streaming data. Direct P2P coordination, session-ticket issuance, and relay are planned; they are not yet implemented end to end by the server.

## Implementation status

| Capability | Status |
| --- | --- |
| Go server configuration, trusted-proxy mTLS identity, device registry, WebSocket presence and API | Implemented foundation |
| Protocol definition for sync records, P2P tickets, relay and data frames | Defined; server handlers are incomplete |
| Windows certificate selection, control-plane client, clipboard/placeholder handling, Range download and LAN/VPN race connector | Client modules implemented; server-side sync/P2P integration remains incomplete |
| Android KeyChain mTLS, share intents, PFD-based streaming sender, BLAKE3 framing and foreground service | Client modules implemented; requires a compatible receiving peer and P2P signaling |
| End-to-end sync, P2P ticket exchange, interoperable peer listener, relay and complete download flow | Not yet complete |

The Android client currently sends shared files; it does not yet implement a receiver or clipboard text synchronization. Its P2P connector needs server signaling and a peer listener before direct transfer can work. The server currently supports device registration, heartbeat, ping and presence; protocol messages for record publishing, P2P offers and relay are not all handled.

## Quick start with Docker

The server image is published to GitHub Container Registry:

```sh
docker pull ghcr.io/molirain/molisync-server:latest
```

Tags: `latest` (default branch), `vX.Y.Z` and `X.Y` for release tags, and `sha-<commit>` for every published build. Images are multi-arch (`linux/amd64`, `linux/arm64`), run as `nonroot`, and contain no shell or package manager.

Start from the example Compose file:

```sh
cp docker-compose.example.yml docker-compose.yml
docker compose up -d
docker compose ps
```

The Compose file starts only the control plane on the private `molisync-internal` network (subnet `172.28.0.0/24`) and does **not** publish port 9520. Your existing reverse proxy (Nginx, NPM, Caddy, ...) must join that network, terminate mTLS, verify the client certificate, strip any inbound `X-Client-*` headers, and set fresh verified values:

```nginx
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection $connection_upgrade;
proxy_request_buffering off;
proxy_buffering off;

proxy_set_header X-Client-Verify $ssl_client_verify;
proxy_set_header X-Client-Fingerprint $ssl_client_fingerprint;
proxy_set_header X-Client-DN $ssl_client_s_dn;
```

Key container settings (all can be passed as environment variables; see [`server/config.example.yaml`](server/config.example.yaml) for the full list):

| Variable | Purpose |
| --- | --- |
| `MOLISYNC_SERVER_LISTEN` | Listen address inside the container. The image defaults to `0.0.0.0:9520`; never publish it to the internet. |
| `MOLISYNC_PROXY_TRUSTED_CIDRS` | Proxy CIDRs allowed to assert an identity. Must match the Compose subnet, and only the proxy should join that network. |
| `MOLISYNC_PROXY_VERIFY_HEADER` / `..._FINGERPRINT_HEADER` / `..._DN_HEADER` | Header names the proxy uses for the verified result, certificate fingerprint and subject. |
| `MOLISYNC_AUTH_DEVICE_ID_SOURCE` | `fingerprint` (recommended) or `dn_cn`. |
| `MOLISYNC_LOG_LEVEL` / `MOLISYNC_LOG_FORMAT` | `debug`/`info`/`warn`/`error` and `json`/`text`. |

Notes:

- **Anyone who can reach the service on the trusted network can forge the identity headers.** Keep `molisync-internal` limited to the proxy and the server.
- To use a YAML configuration instead of environment variables, mount it read-only and pass `-config`: `./server/config.yaml:/config.yaml:ro` plus `command: ["-config", "/config.yaml"]`. Environment variables still win over the file.
- The image health check calls the binary's `-healthcheck-url` probe against `http://127.0.0.1:9520/healthz`. If you change the listen port, override the Compose health check too.
- Private GHCR packages must be pulled after `docker login ghcr.io` or made public in the package settings.

Build the image locally:

```sh
cd server
docker build -t molisync-server:dev .
docker run --rm -p 127.0.0.1:9520:9520 \
  -e MOLISYNC_PROXY_TRUSTED_CIDRS=127.0.0.1/32 \
  molisync-server:dev
```

## Security model

- Never trust client-provided identity headers. Configure `proxy.trusted_cidrs` narrowly and bind the service to loopback or a private network.
- Configure the reverse proxy to verify client certificates (including revocation policy where applicable), strip inbound identity headers, and set fresh verified values.
- Do not expose the Go service directly to the public internet when relying on proxy-authenticated identity.
- The container runs as `nonroot` with a read-only root filesystem, no added capabilities and `no-new-privileges` in the example Compose file.
- Android and Windows clients use OS-managed certificate/private-key stores; client private keys are not exported to application files.
- Android requires HTTPS, disables cleartext traffic and HTTP redirects for the mTLS client, and uses a pinned private CA when `molisync_ca.crt` is packaged.
- Review each platform README and deployment configuration before exposing a service. This project is under active development and is not yet a complete production sync/relay system.

## Requirements

- **Server:** Debian/Linux, Go 1.22 or newer, or Docker 24+ for the container image.
- **Windows:** Windows 10/11, .NET 8 SDK. NativeAOT publishing additionally requires the Visual C++ build tools.
- **Android:** Android 8.0 / API 26 or newer, JDK 17+, Android SDK Platform 35 and Build-Tools 35.0.0.

## Build and test

### Server

```sh
cd server
go test ./...
go vet ./...
go build -o bin/molisync-server ./cmd/server
```

Use [`server/config.example.yaml`](server/config.example.yaml) as the starting point. The service supports YAML plus `MOLISYNC_*` environment variables; configure trusted proxy CIDRs and proxy header names before running it.

### Windows client

```powershell
cd client-windows
dotnet build MoliSync.Client.sln -c Release
dotnet test MoliSync.Client.sln -c Release
dotnet publish src/MoliSync.Client/MoliSync.Client.csproj -c Release -r win-x64 -o publish
```

Copy [`client.example.json`](client-windows/client.example.json) to `client.json`, then configure the HTTPS server URL and CA certificate path. See the [Windows client guide](client-windows/README.md).

### Android client

```sh
cd client-android
./gradlew :app:assembleDebug
./gradlew :app:testDebugUnitTest
./gradlew :app:lintDebug
```

On Windows, use `gradlew.bat`. See the [Android client guide](client-android/README.md) for KeyChain setup, private CA configuration, share handling and release signing.

## Continuous integration and releases

| Workflow | Trigger | Output |
| --- | --- | --- |
| [`android.yml`](.github/workflows/android.yml) | PRs; pushes to `main`/`master`; `v*` tags | Build/test/lint; signed APK attached to a GitHub Release on version tags |
| [`server-image.yml`](.github/workflows/server-image.yml) | Changes under `server/`; `v*` tags | `go vet` + `go test -race`, then a multi-arch image pushed to GHCR |

Android release signing requires these repository secrets: `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD`. See the Android guide for preparing them. Never commit a release keystore or its passwords.

## Documentation

- [Server setup and protocol behavior](server/README.md)
- [Windows client](client-windows/README.md)
- [Android client](client-android/README.md)
- [Protocol definition](proto/sync.proto)
- [Architecture whitepaper (Chinese)](MoliSync%20分布式跨端同步与点对点流式互传系统技术架构白皮书.md)

## License

No repository-wide license has been declared yet. Until a license is added, do not assume that source code may be redistributed or reused.
