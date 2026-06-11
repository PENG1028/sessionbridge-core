# SessionBridge Core

Go-based mesh node for distributed capability execution, peer-to-peer relay, and cross-device development synchronization.

## Quick Start

```bash
# Download pre-built binary
# https://github.com/PENG1028/sessionbridge-core/releases/latest

# Run (first run auto-generates config, identity, and auth token)
./sessionnode
```

## What It Does

- **File system** — read, write, list, stat across the node's filesystem
- **Process management** — spawn, signal, resize, and list processes with full tree tracking
- **Terminal sessions** — create PTY-backed shell sessions, stream I/O over WebSocket
- **Clipboard** — read and write system clipboard across platforms
- **Screen capture** — screenshot desktops for AI perception
- **Mesh networking** — ed25519-authenticated peer-to-peer connections between nodes
- **Invite pairing** — one-time code-based trust establishment for adding peers
- **Forward-Only Hub mode** — run a relay node that forwards without local execution
- **Stream relay** — subscribe, replay, and tail session output streams
- **Run lifecycle** — create, attach, stop runs with configurable disconnect/shutdown policies
- **Self-update awareness** — check and plan updates from git or release sources

## Capabilities

SessionBridge Core registers **89 capability handlers** (and growing), organized into:

| Area | Count | Examples |
|------|-------|---------|
| Session / Stream | 10 | `session.create`, `stream.subscribe` |
| Filesystem | 7 | `fs.read`, `fs.write`, `fs.list` |
| Process | 4 | `process.spawn`, `process.signal` |
| Environment | 8 | `env.get`, `env.which`, `env.cwd` |
| Config | 4 | `config.get`, `config.set` |
| Node / Peer / Mesh | 18 | `node.list`, `node.peer.reconnect`, `node.invite.create` |
| History / Observability | 9 | `session.history.stats`, `logs.tail`, `audit.list` |
| Run / Task | 8 | `run.create`, `task.list` |
| Operations | 6 | `operations.dryRun`, `operations.rollback` |
| Update | 8 | `update.check`, `update.plan` |
| Desktop (AI) | 3 | `desktop.clipboard.get`, `desktop.screenshot` |
| Notify / Approval | 4 | `notify.send`, `approval.list` |

## Architecture

```
cmd/node          Entry point for the sessionnode binary
internal/
  server/         HTTP + WebSocket server (/ws, /peer/ws, /peer/invite/accept)
  executor/       Capability handler registry (89 handlers)
  dispatcher/     8-step capability dispatch chain
  session/        Session lifecycle and store
  process/        OS process management + PTY
  run/            High-level run abstraction over sessions
  mesh/           Trust store, invite codes, peer pairing
  topology/       Node discovery and mesh routing
  wsconn/         WebSocket connection registry
  config/         Node configuration
  history/        Session history persistence
  permission/     Capability permission model
  plan/           Multi-step execution plans
  notify/         Notification dispatch
  auth/           Token and ed25519 authentication
  capability/     Platform capability support matrix
  crypto/         Encryption primitives (ECDH, AES-GCM)
  update/         Self-update source and policy
  logs/           Structured logging and auditing
  oplog/          Operation log for rollback recovery
pkg/
  protocol/       Wire protocol messages and error codes
  types/          Shared type definitions
```

## Endpoints

| Endpoint | Auth | Purpose |
|---|---|---|
| `/ws` | SESSIONNODE_TOKEN | Control WebSocket (App UI / clients) |
| `/peer/ws` | ed25519 handshake | Core-to-Core mesh connection |
| `/peer/invite/accept` | Invite code | One-time peer pairing |
| `/health` | None | Health check |

## Configuration

```bash
# Environment variables
SESSIONNODE_TOKEN=your-token        # Required for /ws auth
LISTEN_ADDR=127.0.0.1:9090          # Listen address
SESSIONNODE_DATA_DIR=~/.sessionnode # Data directory

# Config file: ~/.sessionnode/config.json
# First run auto-creates with sensible defaults.
```

## Building

```bash
go build ./cmd/node/
# → ./sessionnode (or sessionnode.exe on Windows)
```

Cross-platform build (no CGo required):

```bash
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/node/
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/node/
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/node/
```

## Testing

```bash
go test ./...
```

## Design Documents

| Document | Scope |
|---|---|
| [CORE_CHANGES.md](CORE_CHANGES.md) | Overall specification: Hub mode, capability trimming, admin endpoints, config changes |
| [ENCRYPTION.md](ENCRYPTION.md) | Encryption architecture: message encryption, Noise protocol, E2E blind relay, TLS |
| [DEPLOY.md](DEPLOY.md) | Zero-config deployment: release self-update, relay auto-register, mDNS discovery |
| [AI_EXTENSION.md](AI_EXTENSION.md) | AI computer-use extension: analysis, clipboard/screenshot, browser/perception roadmap |

## License

MIT
