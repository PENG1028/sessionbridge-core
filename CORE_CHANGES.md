# SessionBridge Core → 跨平台开发同步核心 改动规格书

> 本文档是给另一个 AI 实施用的完整改动清单。
> 基于 [sessionbridge-core](https://github.com/PENG1028/sessionbridge-core) fork 修改。

---

## 目录

1. [改动 1：Forward-Only Hub Mode（安全核心）](#改动-1forward-only-hub-mode安全核心)
2. [改动 2：消息协议扩展 — relay.policy](#改动-2消息协议扩展--relaypolicy)
3. [改动 3：能力裁剪 — 终端相关能力可禁用](#改动-3能力裁剪--终端相关能力可禁用)
4. [改动 4：新增开发同步能力（可选，按需实现）](#改动-4新增开发同步能力可选按需实现)
5. [改动 5：Admin 管理端点](#改动-5admin-管理端点)
6. [改动 6：配置变更汇总](#改动-6配置变更汇总)
7. [未来考虑：多租户 Relay 层](#未来考虑多租户-relay-层)
8. [实现顺序建议](#实现顺序建议)

---

## 改动 1：Forward-Only Hub Mode（安全核心）

**目标**：让一个 Core 节点作为纯转发中继，绝不本地执行来自远端 peer 的能力请求。

### 1.1 Config 层 — 添加 HubMode 标志

**文件**：`internal/config/config.go`

在 `NodeConfig` 结构体中添加字段：

```go
// NodeConfig identifies the local node.
type NodeConfig struct {
    Name string `json:"name"`
    Role string `json:"role"` // "standalone","relay","leaf","hub"
    
    // HubMode 开关：当设为 true 时，节点只转发不执行。
    // 这是 "role":"hub" 的显式版，两者任一为 true 即生效。
    HubMode bool `json:"hubMode,omitempty"`
}
```

在 `defaultConfig()` 中不需要改（默认为 false）。

### 1.2 Topology 层 — 拦截本地执行

**文件**：`internal/topology/topology.go`

#### 1.2.1 Config 结构体加字段

```go
type Config struct {
    LocalID              types.NodeID
    LocalName            string
    Identity             *mesh.NodeIdentity
    Peers                []PeerConfig
    InboundPeerReachable bool
    
    // ForwardOnly 开关：true = 只转发不本地执行
    ForwardOnly bool
}
```

#### 1.2.2 HandleMessage 中拦截非法执行

找到 `handleMeshCall` 函数（~line 502）：

```go
func (pt *PeerTopology) handleMeshCall(senderID types.NodeID, msg *protocol.Message) {
    // ==== 新增：ForwardOnly 检查 ====
    if pt.forwardOnly {
        // 检查请求目标是否为本地节点：TargetNodeID 为空或等于本机 ID
        targetIsLocal := msg.TargetNodeID == "" || msg.TargetNodeID == pt.localID
        if targetIsLocal {
            pt.log.Printf("FORWARD-ONLY: rejecting mesh.call %s from %s — hub does not execute capabilities", msg.Capability, senderID)
            // 发回错误响应
            errResp := &types.CapabilityResponse{
                RequestID: msg.RequestID,
                OK:        false,
                Error:     &types.CoreError{
                    Code:    "HUB_MODE_REJECTED",
                    Message: "this node is in forward-only mode and does not execute capabilities",
                },
            }
            resultMsg := protocol.NewMeshResult(errResp)
            data, _ := resultMsg.MarshalJSON()
            // 写回 sender 的 writeCh...
            return
        }
    }
    // ==== 结束 ====
    
    // 原有逻辑继续：创建 req -> d.Dispatch(req)
    // ...
}
```

同样，在 `HandleMessage` 的 `MsgTypeActionRequest` 分支（~line 443）也加同样的拦截。

对于 `MsgTypeActionRequest`，判断条件也一样：

```go
case protocol.MsgTypeActionRequest:
    // ==== 新增：ForwardOnly 检查 ====
    if pt.forwardOnly {
        targetIsLocal := msg.TargetNodeID == "" || msg.TargetNodeID == pt.localID
        if targetIsLocal {
            pt.log.Printf("FORWARD-ONLY: rejecting action.request %s from %s", msg.Capability, senderID)
            // 直接向 sender 返回错误响应
            errResp := &types.CapabilityResponse{
                RequestID: msg.RequestID,
                OK:        false,
                Error: &types.CoreError{
                    Code:    "HUB_MODE_REJECTED",
                    Message: "this node is in forward-only mode and does not execute capabilities",
                },
            }
            resultMsg := protocol.NewActionResponse(errResp)
            data, _ := resultMsg.MarshalJSON()
            // 写回 sender 的 writeCh...
            return
        }
    }
    // ==== 结束 ====
    
    // 原有 Dispatch 逻辑继续…
```

**关键逻辑**：只拦截那些"目标是本机"的请求。如果请求指明了远端 `TargetNodeID`（不是本机），则正常转发。这样 hub 的路由功能不受影响。

#### 1.2.3 New 函数接受 ForwardOnly 参数

```go
func New(cfg Config) *PeerTopology {
    pt := &PeerTopology{
        localID:              cfg.LocalID,
        localName:            cfg.LocalName,
        identity:             cfg.Identity,
        inboundPeerReachable: cfg.InboundPeerReachable,
        forwardOnly:          cfg.ForwardOnly,  // 新增
        // ...
    }
    // ...
}
```

`PeerTopology` 结构体新增字段：

```go
type PeerTopology struct {
    // ... 现有字段 ...
    forwardOnly bool  // 新增
}
```

### 1.3 Main.go — 传递 HubMode

**文件**：`cmd/node/main.go`

在构建 `topology.Config` 的地方：

```go
topoCfg := topology.Config{
    LocalID:              nodeID,
    LocalName:            cfg.Node.Name,
    Identity:             nodeIdentity,
    Peers:                peers,
    InboundPeerReachable: inboundPeerReachable,
    ForwardOnly:          cfg.Node.HubMode || cfg.Node.Role == "hub",  // 新增
}
```

**逻辑**：`hubMode: true` 或 `role: "hub"` 任一成立即启用。

### 1.4 安全旁路 — 本地管理员通道

ForwardOnly 模式下，管理员自己还需要能管理这个 hub 节点（查看连接状态、配置、日志）。

方案：在 `handlePeerWS` 中保留一个例外——如果连接的 peer 是"管理员身份"（另一种信任策略），允许其执行受限能力。

具体来说，在信任存储中加一个 `Policy.Mode` 检查：

```go
// handleMeshCall 中
if pt.forwardOnly {
    targetIsLocal := msg.TargetNodeID == "" || msg.TargetNodeID == pt.localID
    if targetIsLocal {
        // ==== 新增：管理员例外 ====
        // 允许 "admin" 角色的 peer 执行内置管理能力
        if pt.isAdminPeer(senderID) && isAdminCapability(msg.Capability) {
            // 放行，不走拒绝逻辑
        } else {
            // 拒绝
            return
        }
        // ==== 结束 ====
    }
}
```

管理员能力的范围：

```go
var adminCapabilities = map[string]bool{
    "config.list":    true,
    "config.get":     true,
    "node.list":      true,
    "node.info":      true,
    "node.health":    true,
    "logs.tail":      true,
    "logs.query":     true,
    "audit.list":     true,
    "peer.list":      true,
    "peer.info":      true,
    "update.status":  true,
}
```

**实现要点**：
- 信任策略（`TrustPolicy`）中现有 `Mode` 字段，加一个值 `"admin"`
- `isAdminPeer()` 检查发送者的信任策略是否为 admin
- `isAdminCapability()` 检查能力名是否在允许列表里

---

## 改动 2：消息协议扩展 — relay.policy

**目标**：当 hub 节点负载过高时，能广播策略通知给连接的 leaf，让 leaf 自行降频。

### 2.1 协议消息类型

**文件**：`pkg/protocol/message.go`

添加常量：

```go
const (
    MsgTypeRelayPolicy = "relay.policy"
    // 其他消息类型...
)
```

新建消息结构（或复用现有 Message 结构）：

```go
// RelayPolicyPayload 由 hub 广播给所有连接的 leaf
type RelayPolicyPayload struct {
    MaxConnections  int    `json:"maxConnections,omitempty"`  // 0 = 不限制
    RateLimit       string `json:"rateLimit,omitempty"`       // "100req/min"
    Features        []string `json:"features,omitempty"`      // 可用功能列表
    Status          string `json:"status"`                    // "normal" | "busy" | "maintenance"
    RetryAfter      int    `json:"retryAfter,omitempty"`      // 秒，busy 时建议重试间隔
    Message         string `json:"message,omitempty"`         // 人类可读的原因
}
```

### 2.2 Hub 端发送策略

**文件**：`internal/topology/topology.go` 或新建 `internal/topology/policy.go`

新增 `BroadcastPolicy` 方法：

```go
// BroadcastPolicy 向所有连接的 peer 广播当前策略
func (pt *PeerTopology) BroadcastPolicy(payload *RelayPolicyPayload) {
    data, _ := json.Marshal(payload)
    msg := &protocol.Message{
        Type:    protocol.MsgTypeRelayPolicy,
        Payload: data,
    }
    msgData, _ := msg.MarshalJSON()
    
    pt.mu.RLock()
    defer pt.mu.RUnlock()
    
    for _, peer := range pt.peers {
        if peer.ID == pt.localID {
            continue
        }
        peer.mu.RLock()
        ch := peer.writeCh
        peer.mu.RUnlock()
        if ch != nil {
            select {
            case ch <- msgData:
            default:
                pt.log.Printf("policy broadcast to %s: channel full", peer.ID)
            }
        }
    }
}
```

### 2.3 Leaf 端处理策略

**文件**：`internal/topology/topology.go` 的 `HandleMessage` 中

```go
case protocol.MsgTypeRelayPolicy:
    var policy RelayPolicyPayload
    json.Unmarshal(msg.Payload, &policy)
    pt.log.Printf("relay policy updated: status=%s", policy.Status)
    // 存储策略供后续限速参考
    pt.mu.Lock()
    pt.relayPolicy = &policy
    pt.mu.Unlock()
    return
```

`PeerTopology` 加字段：

```go
type PeerTopology struct {
    // ... 现有字段 ...
    relayPolicy *RelayPolicyPayload  // 新增：hub 广播的策略
    relayPolicyMu sync.RWMutex
}
```

### 2.4 服务端负载检测

**文件**：`internal/server/server.go`

在 `Start()` 中启动一个 goroutine，定期检查负载并广播策略：

```go
// 在 Start() 中新增
if s.forwardOnly {
    go s.monitorLoad(ctx)
}

func (s *Server) monitorLoad(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // 计算当前连接数
            connCount := s.connRegistry.Count()
            
            policy := &RelayPolicyPayload{
                Status: "normal",
            }
            
            // 根据连接数判断负载
            if connCount > 100 {
                policy.Status = "busy"
                policy.RetryAfter = 5
                policy.Message = "high connection load, please retry later"
            }
            if connCount > 200 {
                policy.Status = "busy"
                policy.RetryAfter = 30
                policy.RateLimit = "10req/min"
                policy.Message = "node at capacity, rate limiting active"
            }
            
            // 只在状态变化时广播
            if policy.Status != s.lastPolicyStatus {
                s.lastPolicyStatus = policy.Status
                s.broadcastPolicy(policy)
            }
        }
    }
}
```

> **注意**：具体阈值应在配置中可调，这里只是示例。

---

## 改动 3：能力裁剪 — 终端相关能力可禁用

**目标**：新项目的 Core（非终端场景）不需要 PTY/终端能力，但需要保留 session/stream/fs/env 等。

### 3.1 按运行模式注册能力

**文件**：`internal/executor/registry.go`

不需要物理删除终端能力文件，只需在注册时根据模式决定是否注册。

```go
func (r *Registry) registerDefaults() {
    // ==== 始终注册的能力 ====
    r.RegisterSessionCapabilities()
    r.RegisterStreamCapabilities()
    r.RegisterFSCapabilities()
    r.RegisterEnvCapabilities()
    r.RegisterConfigCapabilities()
    r.RegisterSystemCapabilities()
    r.RegisterNodeCapabilities()
    r.RegisterNotificationCapabilities()
    r.RegisterApprovalCapabilities()
    r.RegisterHistoryCapabilities()
    r.RegisterObservabilityCapabilities()
    r.RegisterTaskCapabilities()
    r.RegisterRunCapabilities()
    r.RegisterPeerCapabilities()
    r.RegisterMeshCapabilities()
    r.RegisterUpdateCapabilities()
    
    // ==== 条件注册的能力 ====
    if !r.deps.DisableProcessCaps {
        r.RegisterProcessCapabilities()
    }
}
```

在 `Deps` 中加开关：

```go
type Deps struct {
    // ... 现有字段 ...
    
    // DisableProcessCaps 设为 true 时不注册 process.spawn/signal/resize/list
    DisableProcessCaps bool
    
    // DisableHistoryCaps 设为 true 时不注册历史记录能力
    DisableHistoryCaps bool
}
```

或者在 `New()` 时传一个 feature flag：

```go
type RegistryConfig struct {
    EnableProcess bool
    EnableUpdate  bool
    // ...
}

func New(deps *Deps, cfg RegistryConfig) *Registry {
    // ...
}
```

### 3.2 原文件结构参考

不需要删除 `process_cmds.go` 等文件，只是不注册处理器。能力函数还在，只是 `Execute()` 找不到对应 handler 时报"unknown capability"。

### 3.3 配置暴露

通过 `config.go` 的 `NodeConfig` 或新增的 `RegistryConfig`：

```go
// 在 NodeConfig 中
type NodeConfig struct {
    Name    string `json:"name"`
    Role    string `json:"role"`
    HubMode bool   `json:"hubMode,omitempty"`
    
    // 能力开关
    DisableCaps []string `json:"disableCaps,omitempty"` // 禁用的能力列表
}
```

或者在 `cmd/node/main.go` 中硬编码选择（推荐，因为不同产品就是不同二进制）：

> **推荐做法**：直接 fork 后在 `registerDefaults()` 里注释掉不需要的行。
> 比运行时配置更简单、更安全。

---

## 改动 4：新增开发同步能力（可选，按需实现）

**目标**：为跨设备开发场景新增能力（手机 ↔ PC 同步）。

这些是新能力的示例，根据实际需求取舍：

### 4.1 文件同步

```go
// 增量文件同步（diff + apply）
r.Register("sync.diff", syncDiff)    // 计算本地文件与远程的差异
r.Register("sync.apply", syncApply)  // 应用差异补丁
r.Register("sync.status", syncStatus) // 同步状态

// 文件监听
r.Register("fs.watch", fsWatch)      // 监听文件变更事件
r.Register("fs.unwatch", fsUnwatch)
```

### 4.2 环境同步

```go
r.Register("env.compare", envCompare) // 比较两个节点的环境变量差异
r.Register("env.snapshot", envSnapshot) // 生成环境快照
r.Register("env.restore", envRestore)   // 恢复环境快照
```

### 4.3 命令桥接

```go
r.Register("cmd.execute", cmdExecute)  // 在远端执行命令并返回输出
r.Register("cmd.stream", cmdStream)    // 在远端执行命令并流式输出
```

> 这些能力可以后续按需添加，不需要一次性全部实现。

---

## 改动 5：Admin 管理端点

**目标**：hub 模式下，管理员需要通过 localhost 管理 hub（查看状态、配置、日志）。

### 5.1 新增 admin HTTP 端点

**文件**：`internal/server/server.go`

```go
func (s *Server) registerHandlers() {
    mux := http.NewServeMux()
    mux.HandleFunc("/health", s.handleHealth)
    mux.HandleFunc("/ws", s.handleWS)
    mux.HandleFunc("/peer/ws", s.handlePeerWS)
    mux.HandleFunc("/peer/invite/accept", s.handlePeerInviteAccept)
    
    // ==== 新增：Admin 端点 ====
    mux.HandleFunc("/admin/status", s.handleAdminStatus)    // 节点状态
    mux.HandleFunc("/admin/peers", s.handleAdminPeers)      // 已连接 peer 列表
    mux.HandleFunc("/admin/policy", s.handleAdminPolicy)    // 查看/更新策略
    // ==== 结束 ====
    
    s.httpServer = &http.Server{Addr: s.addr, Handler: mux}
}
```

**重要**：Admin 端点只在 HubMode 下注册，且绑定到 localhost 地址：

```go
func (s *Server) Start() error {
    // 如果 hubMode 且 listen 地址是公网的，额外监听 localhost admin 端口
    if s.hubMode {
        go s.startAdminServer()
    }
    // ... 原有逻辑 ...
}
```

或更简单：Admin 端口单独绑定：

```go
func (s *Server) startAdminServer() {
    adminMux := http.NewServeMux()
    adminMux.HandleFunc("/status", s.handleAdminStatus)
    adminMux.HandleFunc("/peers", s.handleAdminPeers)
    adminMux.HandleFunc("/policy", s.handleAdminPolicy)
    adminAddr := "127.0.0.1:9190" // 只监听 localhost
    log.Printf("[admin] admin server on %s", adminAddr)
    if err := http.ListenAndServe(adminAddr, adminMux); err != nil {
        log.Printf("[admin] admin server error: %v", err)
    }
}
```

### 5.2 Admin 端点实现示例

```go
func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "mode":     "hub",
        "uptime":   time.Since(s.startTime).String(),
        "peers":    len(s.peerConns),
        "sessions": s.sessions.Count(),
    })
}
```

---

## 改动 6：配置变更汇总

### 新增配置项

| 配置路径 | 类型 | 默认值 | 说明 |
|---------|------|--------|------|
| `node.hubMode` | bool | false | hub 模式开关 |
| `node.role` | string | "standalone" | 新增值 `"hub"` |
| `node.disableCaps` | []string | nil | 禁用的能力列表（可选） |

### 环境变量

| 变量 | 说明 |
|------|------|
| `SESSIONNODE_HUB_PORT` | admin 管理端口，默认 9190 |

### 配置项完整示例

```json
{
  "core": {
    "listenAddr": ":9090",
    "dataDir": "~/.sessionnode",
    "auth": { "adminToken": "..." },
    "log": { "level": "info" }
  },
  "node": {
    "name": "my-hub",
    "role": "hub",
    "hubMode": true
  },
  "topology": {
    "peers": [...]
  }
}
```

---

## 未来考虑：多租户 Relay 层

> 这些改动不在当前核心修改范围内，记录在此为后续参考。

### 架构

```
                    ┌──────────────────────┐
                    │   Platform Server     │  ← 新层：管理用户/租户/计费
                    │   (Go/Node.js)        │
                    └──────────┬───────────┘
                               │
                    ┌──────────▼───────────┐
                    │   Hub Core (转发)     │  ← 当前 Fork 的核心
                    │   ForwardOnly=true    │
                    └──────────┬───────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
         ┌────▼────┐     ┌────▼────┐     ┌────▼────┐
         │ 用户 A   │     │ 用户 B   │     │ 用户 C   │
         │ 设备组   │     │ 设备组   │     │ 设备组   │
         └─────────┘     └─────────┘     └─────────┘
```

### Platform Server 负责

1. **用户/账户系统**：注册、登录、订阅管理
2. **身份映射**：将用户账户 → 设备 ed25519 身份
3. **租户隔离**：用户 A 的设备组和用户 B 的设备组彼此不可见
4. **用量计费**：跟踪连接时长/请求量
5. **速率限制**：超出套餐降速不崩溃
6. **连接池**：分组管理，负载过高时排队

### 核心改动

在 Hub Core 的上游添加一个 Platform Server，Hub Core 本身不需要改。Hub Core 对 Platform Server 来说只表示"我是转发器，给我什么请求我就按身份映射转发"。

---

## 实现顺序建议

### Phase 0：最小安全改动（1-2 天）

必须对安全有保障才能发布：

1. **改动 1**：ForwardOnly 模式（config → topology → main.go 连接）
2. **改动 5**：Admin 管理端点（localhost 端口）
3. 验证：公网部署 hub，从远端 peer 发能力请求 → 被拒绝；从本地 admin 端点 → 正常

### Phase 1：能力裁剪（1 天）

4. **改动 3**：禁用终端能力，只注册需要的
5. 清理不需要的 executor 文件引用

### Phase 2：协议层优化（2-3 天）

6. **改动 2**：relay.policy 协议消息
7. 负载监控 goroutine
8. 客户端重试逻辑

### Phase 3：新能力（按需）

9. **改动 4**：文件同步、环境同步等开发相关能力

---

## 文件改动清单（精简版）

| 文件 | 改动 |
|------|------|
| `internal/config/config.go` | `NodeConfig` 加 `HubMode` 字段 |
| `internal/topology/topology.go` | `Config` 加 `ForwardOnly`；`PeerTopology` 加 `forwardOnly`；`handleMeshCall`/`HandleMessage` 加拦截逻辑；`New` 接收新参数 |
| `internal/topology/policy.go` | **新建** — `BroadcastPolicy`、`RelayPolicyPayload` |
| `cmd/node/main.go` | `topology.Config` 传 `ForwardOnly`；读取 `cfg.Node.HubMode` |
| `internal/permission/checker.go` | （可选）加 admin 例外逻辑 |
| `internal/permission/policy.go` | （可选）加 admin role support |
| `internal/server/server.go` | 加 admin 端点 `/admin/*`、`startAdminServer()`、负载监控 |
| `pkg/protocol/message.go` | 加 `MsgTypeRelayPolicy` |
| `internal/executor/registry.go` | 按条件注册能力 |

---

## 给实施 AI 的关键上下文

1. **这个 fork 的目标**：从"终端远程访问"改为"跨设备开发同步平台"
2. **安全第一**：ForwardOnly 模式是核心安全保证，必须先实现
3. **同一个二进制**：公开版和官方版使用同一份代码，区别只在配置 `hubMode: true/false`
4. **不做多租户**：多用户支持在 Platform Server 层（本仓库之外）
5. **参考文件**：阅读 `topology.go` 的 `HandleMessage` 和 `handleMeshCall` 理解请求流转；阅读 `dispatcher.go` 的 8 步管道理解能力分发
