package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	token := "f8d5a5a46edd5e12ebf1321745579fa0a48756986756f14ab419b9b8e1718015"
	u := "ws://43.160.241.180:9090/ws?token=" + token

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 5 * time.Second
	c, httpResp, err := dialer.Dial(u, nil)
	if err != nil {
		fmt.Println("DIAL ERROR:", err)
		if httpResp != nil {
			fmt.Printf("HTTP %d\n", httpResp.StatusCode)
		}
		os.Exit(1)
	}
	defer c.Close()
	fmt.Printf("Connected (HTTP %d)\n", httpResp.StatusCode)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))

	// Test 1: node.list - see if PC peer is in topology
	req1 := map[string]interface{}{
		"type": "action.request", "requestId": "t1",
		"capability": "node.list", "pluginId": "sessionnode-core",
		"actorType": "user", "actorId": "test", "payload": map[string]interface{}{},
	}
	data, _ := json.Marshal(req1)
	c.WriteMessage(websocket.TextMessage, data)
	_, msg, err := c.ReadMessage()
	if err != nil {
		fmt.Println("READ node.list ERROR:", err)
		return
	}
	var r1 struct {
		Payload struct {
			Nodes []struct {
				NodeID string `json:"nodeId"`
				Status string `json:"status"`
			} `json:"nodes"`
		} `json:"payload"`
	}
	json.Unmarshal(msg, &r1)
	fmt.Println("node.list nodes:")
	for _, n := range r1.Payload.Nodes {
		fmt.Printf("  %s  status=%s\n", n.NodeID, n.Status)
	}
	if len(r1.Payload.Nodes) == 0 {
		fmt.Println("ERROR: no nodes in topology - peer not found")
		return
	}

	// Test 2: node.identity.get with targetNodeId=PC
	fmt.Println("\n--- Testing forward with targetNodeId ---")
	req2 := map[string]interface{}{
		"type": "action.request", "requestId": "t2",
		"capability": "node.identity.get", "pluginId": "sessionnode-core",
		"actorType": "user", "actorId": "test",
		"targetNodeId": "294d9778c9a1",
		"payload":      map[string]interface{}{},
	}
	data, _ = json.Marshal(req2)
	c.WriteMessage(websocket.TextMessage, data)
	_, msg, err = c.ReadMessage()
	if err != nil {
		fmt.Println("READ forwarded response ERROR:", err)
		return
	}
	var r2 struct {
		OK      bool                   `json:"ok"`
		Error   *map[string]string     `json:"error"`
		Payload map[string]interface{} `json:"payload"`
	}
	json.Unmarshal(msg, &r2)
	if r2.OK {
		fmt.Printf("FORWARD OK - nodeId=%v\n", r2.Payload["nodeId"])
	} else if r2.Error != nil {
		fmt.Printf("FORWARD FAILED: code=%s msg=%s\n", (*r2.Error)["code"], (*r2.Error)["message"])
	} else {
		fmt.Printf("FORWARD FAILED: %s\n", string(msg)[:300])
	}

	// Test 3: Same request WITHOUT targetNodeId
	fmt.Println("\n--- Testing WITHOUT targetNodeId (should be local) ---")
	req3 := map[string]interface{}{
		"type": "action.request", "requestId": "t3",
		"capability": "node.identity.get", "pluginId": "sessionnode-core",
		"actorType": "user", "actorId": "test",
		"payload": map[string]interface{}{},
	}
	data, _ = json.Marshal(req3)
	c.WriteMessage(websocket.TextMessage, data)
	_, msg, err = c.ReadMessage()
	if err != nil {
		fmt.Println("READ local response ERROR:", err)
		return
	}
	var r3 struct {
		OK      bool                   `json:"ok"`
		Error   *map[string]string     `json:"error"`
		Payload map[string]interface{} `json:"payload"`
	}
	json.Unmarshal(msg, &r3)
	if r3.OK {
		fmt.Printf("LOCAL OK - nodeId=%v\n", r3.Payload["nodeId"])
	} else if r3.Error != nil {
		fmt.Printf("LOCAL FAILED: code=%s msg=%s\n", (*r3.Error)["code"], (*r3.Error)["message"])
	}
}
