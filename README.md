# MoliSync

<img src="assets/branding/molisync-icon-512.png" width="96" alt="MoliSync icon">

**Secure, self-hosted cross-device file transfer and sync — built around mTLS identity and streaming I/O.**

[简体中文](README.zh-CN.md) · English

MoliSync is an open-source project for moving files and synchronized data between devices you control. The repository currently contains a Go control-plane service, a Windows .NET client, and an Android client. The components are under active development; see [Implementation status](#implementation-status) for what is and is not wired end to end.

## Repository layout

| Path | Component |
| --- | --- |
| [`server/`](server/) | Go control-plane service: trusted reverse-proxy authentication, device registry, WebSocket presence and HTTP API. Ships as a container image. |
| [`client-windows/`](client-windows/) | C# / .NET 8 + Avalonia Windows desktop client: automatic certificate-store access, three-tab cloud sync, clipboard/placeholder handling, Range download, image thumbnails and viewer, online-device and IP details, P2P send/receive, data-directory migration and autostart. |
| [`client-android/`](client-android/) | Kotlin / Jetpack Compose Android client: KeyChain mTLS, system share intents, streaming sender, P2P send/receive, foreground data-sync service and MediaStore archiving. |
| [`proto/sync.proto`](proto/sync.proto) | Versioned control-plane and data-plane protocol definition. |
| [`docker-compose.example.yml`](docker-compose.example.yml) | Hardened Compose template for the server container. |
| [`assets/branding/`](assets/branding/) | Shared icon source, PNG renders and the reproducible renderer. |
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
                 Go control plane + durable store
                 presence · records · blobs

   Data plane: race the LAN/VPN candidates for a direct P2P link;
   after 1.2 s fall back to the server's encrypted relay
```

- **Control plane:** clients authenticate to a reverse proxy using client certificates. The proxy validates mTLS and forwards the verification result, certificate fingerprint, and subject to the Go service.
- **Device identity:** by default, the server uses the lowercase SHA-256 fingerprint of the client certificate as the stable `DeviceID`.
- **Proxy trust boundary:** the server accepts identity headers only from configured trusted proxy CIDRs. The proxy must remove client-supplied copies of those headers before setting its own values.
- **Data plane:** the server issues a single-use 32-byte session ticket to both peers. They complete mutual TLS with their system certificates and stream fixed 49-byte frames; when no candidate answers, the server relays the same session with `io.Copy` and never touches disk.
- **Durable cloud records:** text (up to 64 KiB) is stored in SQLite. Images and files are streamed into a blob directory and downloaded with HTTP Range support. Records never expire automatically and can only be deleted by their sender. In Docker both live in the `molisync-data` volume.

## Sync model

| Content | Behaviour |
| --- | --- |
| Text | Up to 64 KiB, always stored on the server, visible to every authorized device, one tap to copy; long text expands into a selectable full-text sheet |
| Image / file ≤ threshold | The client asks every time: store on the server, or send peer-to-peer |
| Image / file > threshold | Peer-to-peer only |
| Default threshold | 15 MiB, adjustable at runtime between 5 and 50 MiB inside the Android client |
| Server retention | Indefinite; no automatic cleanup, no quota yet |
| Download | `GET /api/v1/blobs/{id}` with Range resume |
| Direct P2P | Clients advertise LAN/VPN candidates; the receiver races them (300 ms steps) and falls back to relay after 1.2 s |
| Relay | Transparent byte forwarding only; end-to-end protection stays between the two clients |
| Android archive | Images land in the gallery folder `Pictures/MoliSync`, files in `Download/MoliSync` |

The Android client implements this model in three tabs (text, images, files). The server exposes it as a small HTTP API plus WebSocket notifications:

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/records` | List records (`kind`, `limit`, `before`) |
| `POST /api/v1/records/text` | Store a text record and broadcast it |
| `POST /api/v1/records/blob` | Stream an image/file body (`kind`, `name`, `mime`, optional `X-Blake3`) |
| `GET /api/v1/blobs/{id}` | Download a blob, with Range support |
| `DELETE /api/v1/records/{id}` | Delete your own record and blob |
| `GET /api/v1/relay/{session_id}` | Relay byte stream (`X-Session-Ticket`, hijacked and forwarded both ways) |

## Implementation status

| Capability | Status |
| --- | --- |
| Go server configuration, trusted-proxy mTLS identity, device registry, WebSocket presence and API | Implemented |
| Durable text/image/file records: SQLite metadata, blob directory, Range download, owner-only delete, Docker volume | Implemented |
| Protocol definition for sync records, P2P tickets, relay and data frames | Implemented; `proto/sync.proto` matches the JSON wire format |
| P2P signalling: single-use 32-byte session tickets, candidate pool distribution, relay URL | Implemented |
| P2P relay: connection pairing, `io.Copy` forwarding with no disk writes, 15 s pairing timeout | Implemented |
| Windows P2P listener/sender, Happy Eyeballs candidate race, relay fallback, online devices with IP details | Implemented |
| Android P2P listener/sender, relay fallback, MediaStore archiving, custom device name, long-press actions | Implemented |
| Windows Avalonia desktop UI, automatic Windows-store certificate selection, three-tab cloud sync, image thumbnails and viewer, Range download, data-directory migration and autostart | Implemented |
| Android KeyChain mTLS, three-tab cloud UI, streaming HTTP upload, Range download, share intents, PFD-based sender, BLAKE3 framing and foreground service | Implemented |
| End-to-end P2P sync, ticket exchange, interoperable peer listener, relay | Implemented on both clients; still needs real-network regression |
| Client-side update check | Deferred |

Text sync across devices runs through the server. Images and files below the threshold can be stored on the server and downloaded by any authorized client, or sent directly peer-to-peer. When no candidate is reachable the clients fall back to the server relay, and the session ticket is still verified on the receiving side. Both desktop and mobile clients let you rename the device and inspect every LAN/VPN address of an online peer.

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
| `MOLISYNC_STORAGE_DATA_DIR` | SQLite database and default blob root. The image uses `/var/lib/molisync`, backed by the `molisync-data` volume. |
| `MOLISYNC_STORAGE_MAX_ITEM_BYTES` | Maximum size of one uploaded blob, default 64 MiB. |
| `MOLISYNC_LOG_LEVEL` / `MOLISYNC_LOG_FORMAT` | `debug`/`info`/`warn`/`error` and `json`/`text`. |

Notes:

- **Anyone who can reach the service on the trusted network can forge the identity headers.** Keep `molisync-internal` limited to the proxy and the server.
- To use a YAML configuration instead of environment variables, mount it read-only and pass `-config`: `./server/config.yaml:/config.yaml:ro` plus `command: ["-config", "/config.yaml"]`. Environment variables still win over the file.
- The image health check calls the binary's `-healthcheck-url` probe against `http://127.0.0.1:9520/healthz`. If you change the listen port, override the Compose health check too.
- Private GHCR packages must be pulled after `docker login ghcr.io` or made public in the package settings.
- Records and blobs persist across container rebuilds in the named volume `molisync-data`. Back up that volume (or at least the SQLite database and `blobs/` directory); deleting it deletes all synced content. Compose sets `read_only: true` for the container, so nothing is written outside the volume.

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

Run `publish\MoliSync.Client.exe` and enter the HTTPS server URL; the client certificate is read automatically from the Windows personal certificate store. If the server uses a private CA, import that CA once into Windows “Trusted Root Certification Authorities”. See the [Windows client guide](client-windows/README.md).

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

The same sync-and-file mark is used for the Android launcher/notification icon, the Windows executable icon and this page. The server exposes only HTTP/WebSocket APIs, so it has no graphical surface to brand; [`assets/branding/`](assets/branding/) keeps the shared source for future UIs.

## License

No repository-wide license has been declared yet. Until a license is added, do not assume that source code may be redistributed or reused.
