#!/bin/bash
# Go Core — VPS E2E Quick Test
#
# Builds, deploys to VPS, verifies connectivity + forwarding.
#
# Environment variables:
#   VPS_HOST     (default: 43.160.241.180)
#   VPS_USER     (default: ubuntu)
#   VPS_PASS     (password, or leave unset for SSH key)
#   VPS_PORT     (default: 9090)
#   LOCAL_PORT   (default: 9191)
#
# Usage:
#   export VPS_PASS="your_password"
#   bash test-vps-e2e.sh

set -euo pipefail

VPS_HOST="${VPS_HOST:-43.160.241.180}"
VPS_USER="${VPS_USER:-ubuntu}"
VPS_PASS="${VPS_PASS:-}"
VPS_PORT="${VPS_PORT:-9090}"
LOCAL_PORT="${LOCAL_PORT:-9191}"
GO_CORE_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="/tmp/go-core-e2e"
PASS_CMD=""

[ -n "$VPS_PASS" ] && PASS_CMD="sshpass -p '$VPS_PASS'"

SSH="$PASS_CMD ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null ${VPS_USER}@${VPS_HOST}"
SCP="$PASS_CMD scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"

cleanup() {
  echo "--- Cleanup ---"
  eval "$SSH 'pkill -f /tmp/go-core-node 2>/dev/null; rm -f /tmp/go-core-node /tmp/sessionnode-e2e -rf' || true"
  pkill -f go-core-node.exe 2>/dev/null || true
  rm -rf "$BIN_DIR"
}
trap cleanup EXIT

echo "=== Go Core VPS E2E Test ==="
echo ""

# ---- Build ----
echo "[1/6] Cross-compile for linux/amd64..."
mkdir -p "$BIN_DIR"
cd "$GO_CORE_DIR"
GOOS=linux GOARCH=amd64 /c/go/bin/go.exe build -o "$BIN_DIR/go-core-node" ./cmd/node/
echo "      Done: $BIN_DIR/go-core-node"

echo "[2/6] Build Windows binary..."
/c/go/bin/go.exe build -o "$BIN_DIR/go-core-node.exe" ./cmd/node/

# ---- Deploy ----
echo "[3/6] Deploy to VPS..."
eval "$SCP '$BIN_DIR/go-core-node' ${VPS_USER}@${VPS_HOST}:/tmp/go-core-node"
eval "$SSH 'chmod +x /tmp/go-core-node'"

# ---- Start remote ----
echo "[4/6] Start remote node on :$VPS_PORT..."
eval "$SSH 'pkill -f /tmp/go-core-node 2>/dev/null; sleep 0.5
  NODE_ID=vps-node \
  LISTEN_ADDR=:$VPS_PORT \
  SESSIONNODE_DATA_DIR=/tmp/sessionnode-e2e \
  nohup /tmp/go-core-node > /tmp/go-core-node.log 2>&1 &
  echo started'"
sleep 2

# ---- Health check ----
echo "[5/6] Health check..."
HEALTH=$(eval "$SSH 'curl -sf http://127.0.0.1:$VPS_PORT/health'")
echo "      Remote health: $HEALTH"

if ! echo "$HEALTH" | grep -q '"status":"ok"'; then
  echo "      FAIL: remote health check"
  eval "$SSH 'cat /tmp/go-core-node.log'"
  exit 1
fi
echo "      PASS"

# ---- WS test (uses a tiny Go program) ----
echo "[6/6] WebSocket forwarding test..."
cat > "$BIN_DIR/ws_test.go" <<'GOEOF'
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
	"github.com/gorilla/websocket"
)

func main() {
	port := os.Getenv("LOCAL_PORT")
	if port == "" { port = "9191" }
	addr := fmt.Sprintf("ws://127.0.0.1:%s/ws", port)

	conn, _, err := websocket.DefaultDialer.Dial(addr, nil)
	if err != nil { fmt.Printf("DIAL_ERROR:%s\n", err); os.Exit(1) }
	defer conn.Close()

	// 1. Ping
	conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`))
	conn.SetReadDeadline(time.Now().Add(5*time.Second))
	_, raw, _ := conn.ReadMessage()
	var m map[string]interface{}
	json.Unmarshal(raw, &m)
	if m["type"] != "pong" { fmt.Printf("NO_PONG:%s\n", raw); os.Exit(1) }
	fmt.Println("  ping/pong OK")

	// 2. node.list
	req := `{"type":"action.request","requestId":"e2e_nodes","pluginId":"sessionnode-core","capability":"node.list"}`
	conn.WriteMessage(websocket.TextMessage, []byte(req))
	conn.SetReadDeadline(time.Now().Add(10*time.Second))
	_, raw, _ = conn.ReadMessage()
	json.Unmarshal(raw, &m)
	if m["ok"] != true { fmt.Printf("NODE_LIST_FAIL:%v\n", m["error"]); os.Exit(1) }
	pb, _ := json.Marshal(m["payload"])
	var p map[string]interface{}
	json.Unmarshal(pb, &p)
	nodes, _ := p["nodes"].([]interface{})
	found := false
	for _, n := range nodes {
		nd := n.(map[string]interface{})
		id := nd["nodeId"].(string)
		fmt.Printf("  node %s status=%s\n", id, nd["status"])
		if id == "vps-node" && nd["status"] == "connected" { found = true }
	}
	if !found { fmt.Println("VPS_NODE_NOT_CONNECTED"); os.Exit(1) }
	fmt.Println("  VPS peer connected")

	// 3. Forward system.info to VPS
	fwd := `{"type":"action.request","requestId":"e2e_fwd","pluginId":"sessionnode-core","capability":"system.info","targetNodeId":"vps-node"}`
	conn.WriteMessage(websocket.TextMessage, []byte(fwd))
	conn.SetReadDeadline(time.Now().Add(30*time.Second))
	_, raw, _ = conn.ReadMessage()
	json.Unmarshal(raw, &m)
	if m["ok"] != true { fmt.Printf("FORWARD_FAIL:%v\n", m["error"]); os.Exit(1) }
	pb, _ = json.Marshal(m["payload"])
	json.Unmarshal(pb, &p)
	fmt.Printf("  VPS OS:   %s\n", p["os"])
	fmt.Printf("  VPS Arch: %s\n", p["arch"])
	if p["os"] == nil { fmt.Println("FORWARD_NO_OS"); os.Exit(1) }
	fmt.Println("  Forward OK")

	fmt.Println("ALL_PASSED")
}
GOEOF

# Start local node with VPS as peer
mkdir -p "$BIN_DIR/data"
cat > "$BIN_DIR/config.json" <<CONFIG
{
  "node": { "name": "local-e2e" },
  "core": { "listenAddr": ":$LOCAL_PORT", "dataDir": "$BIN_DIR/data" },
  "topology": {
    "peers": [{ "id": "vps-node", "address": "$VPS_HOST:$VPS_PORT", "tags": ["remote"] }]
  }
}
CONFIG

cd "$GO_CORE_DIR"
SESSIONNODE_CONFIG="$BIN_DIR/config.json" \
SESSIONNODE_DATA_DIR="$BIN_DIR/data" \
NODE_ID="local-node" \
LISTEN_ADDR=":$LOCAL_PORT" \
"$BIN_DIR/go-core-node.exe" > "$BIN_DIR/local-node.log" 2>&1 &
LOCAL_PID=$!
echo "      Local node PID=$LOCAL_PID"
sleep 3

# Run the test
cd "$BIN_DIR"
LOCAL_PORT=$LOCAL_PORT /c/go/bin/go.exe run ws_test.go 2>&1
RESULT=$?
cd "$GO_CORE_DIR"

# Report
echo ""
if [ "$RESULT" = "0" ]; then
  echo "=== ALL VPS E2E TESTS PASSED ==="
else
  echo "=== VPS E2E TEST FAILED (see above) ==="
  echo "Local logs: $BIN_DIR/local-node.log"
  eval "$SSH 'cat /tmp/go-core-node.log'"
fi
exit $RESULT
