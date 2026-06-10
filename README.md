# SessionBridge Core

Go-based mesh node for distributed terminal sessions, process execution, and peer-to-peer command relay.

## Quick Start

```bash
# Install from source
go install github.com/PENG1028/sessionbridge-core/cmd/node@latest

# Or download pre-built binary
# https://github.com/PENG1028/sessionbridge-core/releases/latest

# Run
./sessionnode
```

## What It Does

- **Terminal sessions** — create PTY-backed shell sessions, stream I/O over WebSocket
- **Process management** — spawn, signal, resize, and list processes with full tree tracking
- **File system** — read, write, list, stat across the node's filesystem
- **Mesh networking** — ed25519-authenticated peer-to-peer connections between nodes
- **Invite pairing** — one-time code-based trust establishment for adding peers
- **Stream relay** — subscribe, replay, and tail session output streams
- **Run lifecycle** — create, attach, stop runs with configurable disconnect/shutdown policies
- **Self-update** — check, plan, and apply git-based updates

## Architecture

```
cmd/node          Entry point for the sessionnode binary
internal/
  server/         HTTP + WebSocket server (/ws, /peer/ws, /peer/invite/accept)
  executor/       Capability handler registry (68 capabilities)
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
SESSIONNODE_PLUGIN_DIRS=./plugins   # Plugin directories

# Config file: ~/.sessionnode/config.json
```

## Building

```bash
go build ./cmd/node/
# → ./sessionnode (or sessionnode.exe on Windows)
```

## Testing

```bash
go test ./...
```

## License

MIT
