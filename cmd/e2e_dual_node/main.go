package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

type testCase struct {
	name  string
	send  *protocol.Message
	check func(*protocol.Message) error
}

var nodeLocal = os.Getenv("NODE_LOCAL_ADDR")
var nodeVpsID = os.Getenv("NODE_VPS_ID")
var failCount int

func main() {
	if nodeLocal == "" {
		nodeLocal = "127.0.0.1:9091"
	}
	if nodeVpsID == "" {
		nodeVpsID = "node-vps"
	}

	log.Printf("Dual-node E2E test")
	log.Printf("  node-local (entry):  ws://%s/ws", nodeLocal)
	log.Printf("  node-vps   (remote): %s", nodeVpsID)

	tests := []testCase{
		{
			name: "local execution — system.info without TargetNodeID",
			send: &protocol.Message{
				Type:       protocol.MsgTypeActionRequest,
				RequestID:  "req-local-1",
				PluginID:   "sessionnode-core",
				Capability: "system.info",
				Timestamp:  time.Now().UnixMilli(),
			},
			check: func(resp *protocol.Message) error {
				if !resp.OK {
					return fmt.Errorf("expected OK=true, got OK=%v error=%v", resp.OK, resp.Error)
				}
				var m map[string]interface{}
				if err := json.Unmarshal(resp.Payload, &m); err != nil {
					return fmt.Errorf("payload unmarshal: %w", err)
				}
				if m["goVersion"] == nil {
					return fmt.Errorf("payload missing 'goVersion': %+v", m)
				}
				return nil
			},
		},
		{
			name: "remote routing — system.info with TargetNodeID=node-vps",
			send: &protocol.Message{
				Type:         protocol.MsgTypeActionRequest,
				RequestID:    "req-remote-1",
				PluginID:     "sessionnode-core",
				Capability:   "system.info",
				TargetNodeID: types.NodeID(nodeVpsID),
				Timestamp:    time.Now().UnixMilli(),
			},
			check: func(resp *protocol.Message) error {
				if !resp.OK {
					return fmt.Errorf("expected OK=true, got OK=%v error=%v", resp.OK, resp.Error)
				}
				var m map[string]interface{}
				if err := json.Unmarshal(resp.Payload, &m); err != nil {
					return fmt.Errorf("payload unmarshal: %w", err)
				}
				log.Printf("  remote system.info payload: %+v", m)
				return nil
			},
		},
		{
			name: "unknown target node — should fail",
			send: &protocol.Message{
				Type:         protocol.MsgTypeActionRequest,
				RequestID:    "req-unknown-1",
				PluginID:     "sessionnode-core",
				Capability:   "system.info",
				TargetNodeID: "nonexistent-node",
				Timestamp:    time.Now().UnixMilli(),
			},
			check: func(resp *protocol.Message) error {
				if resp.OK {
					return fmt.Errorf("expected OK=false for unknown target, got OK=true")
				}
				if resp.Error == nil {
					return fmt.Errorf("expected error for unknown target, got nil")
				}
				log.Printf("  expected error: %s: %s", resp.Error.Code, resp.Error.Message)
				return nil
			},
		},
	}

	u := url.URL{Scheme: "ws", Host: nodeLocal, Path: "/ws"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatalf("dial %s: %v", u.String(), err)
	}
	defer c.Close()

	log.Printf("connected to %s", u.String())

	for _, tc := range tests {
		log.Printf("=== %s ===", tc.name)
		data, err := json.Marshal(tc.send)
		if err != nil {
			log.Printf("  FAIL marshal: %v", err)
			failCount++
			continue
		}
		if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("  FAIL write: %v", err)
			failCount++
			continue
		}
		_, raw, err := c.ReadMessage()
		if err != nil {
			log.Printf("  FAIL read: %v", err)
			failCount++
			continue
		}
		resp, err := protocol.UnmarshalMessage(raw)
		if err != nil {
			log.Printf("  FAIL unmarshal: %v", err)
			failCount++
			continue
		}
		if err := tc.check(resp); err != nil {
			log.Printf("  FAIL: %v", err)
			failCount++
		} else {
			log.Printf("  PASS")
		}
		time.Sleep(200 * time.Millisecond)
	}

	if failCount > 0 {
		log.Printf("FAILED: %d / %d tests failed", failCount, len(tests))
		os.Exit(1)
	}
	log.Printf("ALL PASSED (%d / %d)", len(tests)-failCount, len(tests))
}
