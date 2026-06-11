# SessionBridge Core — 加密架构与实现方案

> 本文档定义从当前加密基线到 Tailscale 级安全的渐进式提升路径，
> 以及个人使用场景下的信任分发模型和盲中继架构。

---

## 目录

1. [现状：当前加密基线](#1-现状当前加密基线)
2. [目标：Tailscale 级别加密架构](#2-目标tailscale-级别加密架构)
3. [Phase 1：消息级 AES-256-GCM 加密](#3-phase-1消息级-aes-256-gcm-加密)
4. [Phase 2：Noise 协议替换握手](#4-phase-2noise-协议替换握手)
5. [Phase 3：自动密钥轮换](#5-phase-3自动密钥轮换)
6. [Phase 4：E2E 中继盲加密（Blind Relay）](#6-phase-4e2e-中继盲加密blind-relay)
7. [Phase 5：NAT 穿透 / DERP 中继](#7-phase-5nat-穿透--derp-中继)
8. [盲中继模型：transit 信任模式](#8-盲中继模型transit-信任模式)
9. [TLS 证书与浏览器 PWA 支持](#9-tls-证书与浏览器-pwa-支持)
10. [Relay-CA 模型：自签发节点证书](#10-relay-ca-模型自签发节点证书)
11. [对已有实现的冲突分析](#11-对已有实现的冲突分析)
12. [推荐实施路径](#12-推荐实施路径)

---

## 1. 现状：当前加密基线

### 已实现

| 层 | 实现 | 状态 |
|----|------|------|
| 节点身份 | ed25519 密钥对，持久化到 `identity.json` | ✅ |
| 身份指纹 | SHA-256(publicKey)，hex 编码 | ✅ |
| 密钥持久化 | 0600 权限保存，启动时验证密钥一致性（sign/verify round-trip） | ✅ |
| 节点认证 | 质询-响应握手（服务端发送 32 字节 nonce，客户端 ed25519 签名） | ✅ |
| 信任存储 | TrustedPeer 列表持久化到 `trusted_peers.json`，原子写入带 .bak 回退 | ✅ |
| 信任验证 | Public key 比对、过期/吊销检查 | ✅ |
| 邀请配对 | 一次性邀请码通过 HTTP 交换公钥，双向存储 TrustedPeer | ✅ |

### 未实现（差距）

| 层 | 当前 | Tailscale 级别 |
|----|------|---------------|
| 传输加密 | 纯 WebSocket (ws://)；WSS (wss://) 需手动配置证书 | WireGuard 隧道 |
| 消息负载加密 | 明文 JSON | E2E ChaCha20-Poly1305 |
| 完美前向保密 (PFS) | ❌ 无 | WireGuard ephemeral 密钥，2 分钟轮换 |
| 密钥轮换 | 静态密钥，永不轮换 | 自动轮换 |
| E2E 中继加密 | ❌ 无（Hub 可读所有消息） | DERP 上仍加密 |
| NAT 穿透 | ❌ 无 | STUN + ICE + DERP |
| TLS 证书 | 需手动配置 certFile/keyFile | Let's Encrypt + 自签 CA |

---

## 2. 目标：Tailscale 级别加密架构

### 设计原则

1. **分层正交**：身份层、传输层、消息层各自独立，改一层不影响其他层
2. **零配置优先**：默认无需证书、无需域名、无需 CA，开箱即有传输保密
3. **不妥协的 E2E**：经过 Hub 转发的流量，Hub 无法解密
4. **兼容性第一**：每个 Phase 都提供向后兼容选项

### 整体架构

```
┌─────────────────────────────────────────────────────────┐
│                    应用层 (executor)                      │
│  ┌──────────┬──────────┬──────────┬──────────┐         │
│  │ fs.read  │process.* │ session  │ config   │ ...     │
│  └──────────┴──────────┴──────────┴──────────┘         │
├─────────────────────────────────────────────────────────┤
│                   Dispatch Layer                         │
│     Authenticate → Permission → Plan → Forward/Exec     │
├─────────────────────────────────────────────────────────┤
│                  Message Layer (Phase 1/4)              │
│  ┌─────────────────────────────────────────────────────┐│
│  │  E2E Encrypted Payload (AES-256-GCM / ChaCha20)    ││
│  │  Route headers in plaintext (TargetNodeID, Type)    ││
│  └─────────────────────────────────────────────────────┘│
├─────────────────────────────────────────────────────────┤
│                  Session Layer (Phase 2/3)              │
│  ┌─────────────────────────────────────────────────────┐│
│  │  Noise Protocol Handshake                          ││
│  │  → Mutual auth (ed25519 static keys)                ││
│  │  → Ephemeral X25519 exchange (PFS)                 ││
│  │  → Session key derivation (ChaCha20-Poly1305)      ││
│  │  → Automatic key rotation (every N min / N GB)     ││
│  └─────────────────────────────────────────────────────┘│
├─────────────────────────────────────────────────────────┤
│                  Transport Layer (Phase 5)              │
│  ┌──────────┬──────────┬──────────────────────────┐    │
│  │ ws/wss   │ STUN/ICE │ DERP Relay (E2E tunnel) │    │
│  └──────────┴──────────┴──────────────────────────┘    │
├─────────────────────────────────────────────────────────┤
│                  Identity Layer (已有)                   │
│  ┌─────────────────────────────────────────────────────┐│
│  │  ed25519 key pair                                   ││
│  │  SHA-256 fingerprint                                ││
│  │  TrustedPeer store                                   ││
│  │  Invite-based pairing                               ││
│  └─────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────┘
```

---

## 3. Phase 1：消息级 AES-256-GCM 加密

**目标**：在现有 ed25519 身份层之上，为所有 WebSocket 消息的 Payload 添加加密。
**投入**：~2 天
**依赖**：无（Go 标准库即可，`crypto/aes` + `crypto/cipher` + `crypto/curve25519`）

### 3.1 密钥交换

利用已建立的 ed25519 密钥对进行 X25519 ECDH：

```
NodeA 的私钥 (ed25519) ──→ 转换为 X25519 私钥 ──┐
                                                  ├──→ HKDF → AES-256-GCM send/recv keys
NodeB 的公钥 (ed25519) ──→ 转换为 X25519 公钥 ──┘
```

> ed25519 和 X25519 使用相同的 Curve25519，只需将 ed25519 私钥的种子（seed）
> 和公钥按 X25519 格式重新解释即可完成密钥交换，无需生成额外密钥对。

```go
// 简化实现（使用 crypto/ecdh 或 x/crypto/curve25519）

type SessionCipher struct {
    sendKey   []byte    // AES-256-GCM 发送密钥
    recvKey   []byte    // AES-256-GCM 接收密钥
    sendNonce uint64    // 发送端 nonce 计数器
    recvNonce uint64    // 接收端 nonce 计数器
}

func NewSessionCipher(localPriv, remotePub []byte) *SessionCipher {
    // 参考 RFC 7748：ed25519 私钥种子 → X25519 标量
    // 参考 RFC 7748：ed25519 公钥 → X25519 坐标
    shared := x25519(localPriv, remotePub) // 32 字节共享秘密

    // HKDF 分叉为发送和接收密钥
    kdf := hkdf.New(sha256.New, shared, nil, "sessionbridge-v1")
    sendKey := make([]byte, 32)
    recvKey := make([]byte, 32)
    kdf.Read(sendKey)
    kdf.Read(recvKey)

    return &SessionCipher{sendKey: sendKey, recvKey: recvKey}
}

func (c *SessionCipher) Encrypt(plaintext []byte) (ciphertext, nonce []byte) {
    nonce = make([]byte, 12)
    binary.BigEndian.PutUint64(nonce[4:], atomic.AddUint64(&c.sendNonce, 1))
    block, _ := aes.NewCipher(c.sendKey)
    gcm, _ := cipher.NewGCM(block)
    ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
    return
}

func (c *SessionCipher) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
    block, _ := aes.NewCipher(c.recvKey)
    gcm, _ := cipher.NewGCM(block)
    return gcm.Open(nil, nonce, ciphertext, nil)
}
```

### 3.2 协议层改动

```go
// pkg/protocol/message.go — 新增字段
type Message struct {
    // ... 现有字段不变 ...

    // ==== 新增：加密相关 ====
    EncryptedPayload []byte `json:"encryptedPayload,omitempty"` // AES-256-GCM 加密后的 payload
    EncryptNonce     []byte `json:"encryptNonce,omitempty"`     // 12 字节 nonce
    KeyID            string `json:"keyId,omitempty"`            // 密钥标识（用于轮换）
    // =======================
}
```

**路由字段保持明文**，加密仅作用于 Payload：
```
明文（谁都可见）：     type, requestId, sessionId, nodeId, targetNodeId, keyId
加密（仅收发双方）：   capability, data, payload, actorType, actorId
```

### 3.3 加密/解密介入位置

```
发送路径：
  forward() / dispatchAction()
    → 构建 Message，填充明文
    → cipher.Encrypt(msg.Payload) → msg.EncryptedPayload
    → msg.Payload = nil
    → marshal → write

接收路径：
  HandleMessage()
    → unmarshal
    → msg.EncryptedPayload != nil
    → cipher.Decrypt(msg.EncryptedPayload) → 恢复 msg.Payload
    → 正常处理
```

### 3.4 具体改动文件

| 文件 | 改动 |
|------|------|
| `pkg/protocol/message.go` | 新增 `EncryptedPayload`、`EncryptNonce`、`KeyID` 字段 |
| `internal/crypto/session.go` | **新建** — `SessionCipher` 结构体，ECDH + AES-256-GCM |
| `internal/crypto/keyconv.go` | **新建** — ed25519 ↔ X25519 密钥转换 |
| `internal/server/server.go` | `handlePeerWS` 握手后执行 ECDH，注册 cipher |
| `internal/topology/topology.go` | `peerHandshake` 后执行 ECDH；`forward()` 加密；`HandleMessage` 解密 |
| `internal/topology/peer.go`（新建或现有）| Peer 结构体增加 `cipher *SessionCipher` 字段 |

### 3.5 向后兼容

握手消息中携带加密能力声明：
```go
type peerHelloPayload struct {
    // ... 现有字段 ...
    Capabilities []string `json:"caps,omitempty"` // "encrypt-v1"
}
```

服务端检测到双方都支持 `encrypt-v1` 则启用加密，否则回退到明文。
现有旧节点连接时自动降级为明文，不影响运行。

### 3.6 安全边界

- **对抗被动监听**：✅ AES-256-GCM 加密，窃听者看到的是密文
- **对抗中间人**：✅ 密钥派生需要双方 ed25519 私钥，MITM 无法获取
- **重放攻击**：✅ Nonce 计数器机制确保每条消息 unique
- **密钥泄露后的历史流量**：❌ 无法抵抗（无 PFS，Phase 2 解决）
- **长连接密钥持久化**：✅ 密钥仅存于内存，连接断开即消失

---

## 4. Phase 2：Noise 协议替换握手

**目标**：用 Noise Protocol Framework 替换当前 6 步 ed25519 挑战-响应握手，
一步完成身份认证 + 临时密钥交换 + 双向加密密钥派生。
**投入**：~1 周
**依赖**：`github.com/flynn/noise` 或等价库

### 4.1 为什么用 Noise

| | 当前 ed25519 握手 | Noise 协议 |
|---|---|---|
| 身份认证 | ✅ ed25519 签名 | ✅ ed25519 签名 |
| 密钥交换 | ❌ 无（Phase 1 单独做 ECDH） | ✅ 握手内建 |
| PFS | ❌ | ✅ ephemeral X25519 |
| 消息加密 | ❌ Phase 1 额外层 | ✅ 握手完成后直接可用 |
| 代码复杂度 | 适中 | 中等（用库不复杂） |
| 审计状态 | ✅ 简单，易于审计 | ✅ 行业标准，多个实现已验证 |

### 4.2 握手协议选择

选用 **`Noise_XX_25519_ChaChaPoly_SHA256`**：

- **XX 模式**：双向身份隐藏 + PFS，适合无需 preshared key 的 P2P 场景
- **25519**：X25519 临时密钥
- **ChaChaPoly**：ChaCha20-Poly1305 认证加密
- **SHA256**：哈希函数

### 4.3 握手流程对比

```
当前 7 步 ed25519 握手：

  Client                              Server
    │                                    │
    │ ── peer.hello (nodeId, pubKey) ──→ │
    │                                    │
    │ ← peer.challenge (32B nonce) ───── │
    │                                    │
    │ ── peer.response (sign(nonce)) ──→ │
    │                                    │
    │ ← peer.welcome (nodeId) ────────── │
    │                                    │
    │ 后续：ECDH + AES-256-GCM（Phase 1）│


Noise XX Handshake：

  Client                              Server
    │                                    │
    │ ── msg 1: e, s, sig ────────────→ │  // Client ephemeral + static + signature
    │                                    │
    │ ← msg 2: e, ee, se ────────────── │  // Server ephemeral + ECDH
    │                                    │
    │ ── msg 3: s, es, ss ────────────→ │  // Client static + ECDH
    │                                    │
    │ ← 握手完成 ─────────────────────── │
    │                                    │
    │ 后续所有消息直接加密               │
    │ (不用额外的 encrypt/decrypt 步骤)  │
```

**优势**：
- 3 条消息替代 4 条
- 握手完毕密钥已在手，不需要单独的 Phase 1 加密层
- ChaCha20-Poly1305 比 AES-256-GCM 在移动端更高效

### 4.4 改动范围

| 文件 | 改动 |
|------|------|
| `internal/crypto/noise.go` | **新建** — Noise handshake 封装 |
| `internal/server/server.go:handlePeerWS` | 替换 ~lines 342-485 握手逻辑 |
| `internal/topology/topology.go:peerHandshake` | 替换 ~lines 788-873 握手逻辑 |
| `pkg/protocol/message.go` | 新增 Noise 相关消息类型（可选）|

### 4.5 向后兼容

**方案**：握手第一步带版本协商

```go
type peerHelloPayload struct {
    Version string   `json:"v"`              // "1" = ed25519, "2" = noise
    PubKey  string   `json:"publicKey"`
    Caps    []string `json:"caps,omitempty"` // 能力列表
}
```

服务端逻辑：
```
v=1 → 走原有 ed25519 握手流程
v=2 → 走 Noise 握手流程
```

在过渡期（两个大版本内），新版节点同时支持 `v=1` 和 `v=2`。
新节点优先协商 `v=2`，遇到旧节点自动降级到 `v=1`。

---

## 5. Phase 3：自动密钥轮换

**目标**：即使静态 ed25519 密钥被泄露，历史流量仍不可解密（PFS 真正生效）。
**投入**：~2 天
**依赖**：Phase 2

### 5.1 轮换策略

| 条件 | 触发动作 |
|------|---------|
| 连接持续 ≥15 分钟 | 重新执行 ephemeral 密钥交换 |
| 传输数据 ≥1 GB | 重新执行 ephemeral 密钥交换 |
| 对端主动发起轮换请求 | 立即轮换 |

### 5.2 轮换协议

新增消息类型 `key.rotate`：

```go
const MsgTypeKeyRotate = "key.rotate"

type KeyRotatePayload struct {
    NewEphemeralPub []byte `json:"newEphemeralPub"` // 新的 X25519 公钥
    KeyID           string `json:"keyId"`            // 新密钥标识
}
```

**双缓冲机制**（避免轮换期间的竞态条件）：

```
旧密钥接收窗口：持续 5s 或收到对端 ACK，取两者中先到者
新密钥立即生效

事件序列：
  1. 节点A 发送 key.rotate(newPub, "key-2")
  2. 节点A 立即开始用 key-2 加密发送
  3. 节点B 收到 key.rotate，用新密钥解密
  4. 节点B 回复 key.rotate.ack
  5. 节点A 收到 ack，关闭 key-1 接收窗口
```

---

## 6. Phase 4：E2E 中继盲加密（Blind Relay）

**目标**：经过 Hub 转发的消息，Hub 无法解密——即使 Hub 被攻陷。
**投入**：~1 周
**依赖**：Phase 1（或 Phase 2）+ 改动 1（Forward-Only Hub Mode）

### 6.1 核心原理

消息用**目标节点**的 ed25519 公钥加密，Hub 只有发送方和接收方的公钥，没有私钥：

```
发送方 NodeA                        Hub                           接收方 NodeB
  │                                  │                                │
  │ ECDH(A_priv, B_pub) → keyAB     │                                │
  │                                  │                                │
  │ encrypt(payload, keyAB)          │                                │
  │ msg = {                          │                                │
  │   TargetNodeID: B,              │                                │
  │   SourceNodeID: A,              │  ← 明文路由头（Hub 可读）        │
  │   EncryptedPayload: [...]       │  ← Hub 无法解密                 │
  │   EncryptNonce: [...]           │                                │
  │ }                                │                                │
  │─────────────────────────────────→│                                │
  │                                  │  检查 TargetNodeID ≠ localID   │
  │                                  │  → 盲转发（不读 payload）      │
  │                                  │───────────────────────────────→│
  │                                  │                                │  ECDH(B_priv, A_pub) → keyAB
  │                                  │                                │  decrypt(payload, keyAB)
  │                                  │                                │  → 正常处理
```

### 6.2 密钥来源

关键：**invite 配对时已经交换了 ed25519 公钥**

```go
// NodeA 本地已有 NodeB 的公钥（配对时存储在 TrustStore）
trustedB, _ := TrustStore.Get("NodeB")
sharedKey := ecdh(identity.PrivateKey, trustedB.PublicKey)

// Hub 从未参与密钥交换
// Hub 即使有 NodeA 的公钥和 NodeB 的公钥，也无法派生 sharedKey
// 因为 ECDH(a_priv, b_pub) ≠ ECDH(b_pub, a_pub)
// 实际是 ECDH(a_priv, b_pub) = ECDH(b_priv, a_pub) = sharedKey
// Hub 没有 a_priv 或 b_priv 中的任意一个
```

### 6.3 与 Forward-Only Hub Mode 的关系

```
ForwardOnly Hub + E2E 加密 = 完全盲中继

Hub 的 HandleMessage 逻辑变化：

  msg = unmarshal(data)

  // ==== E2E 加密消息：盲转发 ====
  if len(msg.EncryptedPayload) > 0 && msg.TargetNodeID != "" {
      if msg.TargetNodeID != localID {
          pt.forwardBlind(msg.TargetNodeID, data)  // 原样转发，不解包
          return
      }
      // 目标是自己 → 解密后处理
      plaintext := pt.sessionCipher.Decrypt(msg.EncryptedPayload)
      msg.Payload = plaintext
      // 继续流程...
  }

  // ==== 明文消息：正常处理 ====
  switch msg.Type {
  case protocol.MsgTypeMeshCall:
      pt.handleMeshCall(senderID, msg)
  // ...
  }
```

### 6.4 消息格式

```go
type Message struct {
    // ... 现有路由字段（保持明文）...
    Type         types.MessageType  `json:"type"`
    RequestID    types.RequestID    `json:"requestId"`
    SessionID    types.SessionID    `json:"sessionId"`
    TargetNodeID types.NodeID       `json:"targetNodeId"`
    SourceNodeID types.NodeID       `json:"sourceNodeId"`  // 新增：中转可见发送者

    // 加密负载（E2E 加密后的内容）
    EncryptedPayload []byte `json:"encryptedPayload,omitempty"`
    EncryptNonce     []byte `json:"encryptNonce,omitempty"`

    // 当 EncryptedPayload 为空时使用明文 Payload（向后兼容）
    Payload json.RawMessage `json:"payload,omitempty"`
}
```

### 6.5 安全性论证

| 攻击场景 | 防御 |
|----------|------|
| Hub 被攻陷，攻击者读取所有转发流量 | 看不到明文——E2E 加密使用接收方公钥，攻击者无私钥 |
| Hub 被攻陷，攻击者篡改转发消息 | 篡改会被接收方的 AES-GCM 认证检测到并拒绝 |
| Hub 被攻陷，攻击者冒充发送方 | 无法派生共享密钥（需要发送方私钥） |
| 攻击者记录所有流量，日后获得某节点私钥 | 如果 Phase 3 已实现（定期轮换），历史流量不可解密 |
| Hub 拒绝转发（DoS） | 无法防御——但这是 Hub 运营者本来就有的权力 |

### 6.6 代价

- **Hub 无法做策略检查**：因为看不到内容，Hub 无法对转发内容执行权限检查
- **延迟增加**：加密/解密计算（现代 CPU 上 AES-256-GCM 约 1μs/KB，可忽略）
- **消息体积膨胀**：约 28 字节（12 nonce + 16 GCM tag）每条消息

---

## 7. Phase 5：NAT 穿透 / DERP 中继

**目标**：跨公网设备互联（手机 ↔ PC），无需双方都有公网 IP。
**投入**：2-3 周
**依赖**：Phase 4（E2E 加密确保中继流量保密）

### 7.1 架构

```
                 ┌──────────────┐
                 │  STUN Server │  ← 可自建或用公共 STUN
                 └──────┬───────┘
                        │
  NodeA (NAT后)          │          NodeB (NAT后)
    │                    │            │
    │ ─── STUN → 发现自身公网地址      │
    │                    │            │
    │ ←─── 候选收集 ────────────────→ │
    │  (host: 内网地址)               │
    │  (srflx: 公网地址)              │
    │                    │            │
    │ ──── ICE 连通性检查 ──────────→ │
    │                    │            │
    │  NAT 穿透成功？                  │
    │  ├─ ✅ → 直接 P2P 加密通信       │
    │  └─ ❌ → 走 DERP 中继           │
    │            │                    │
    │            ▼                    │
    │     ┌──────────────┐           │
    │     │  DERP Relay  │ ← 你的 Hub 已适配
    │     │  (E2E加密)   │            │
    │     └──────────────┘            │
```

### 7.2 推荐库

| 功能 | 推荐库 | 理由 |
|------|--------|------|
| STUN | `github.com/pion/stun` | 纯 Go，无 CGo，成熟 |
| ICE | `github.com/pion/ice/v2` | 纯 Go，与 WebRTC 兼容 |
| 中继协议 | 自定义 DERP 简化版 | 比 WireGuard 内核模块简单 |

### 7.3 Hub 作为 DERP Relay

Forward-Only Hub 天生适合做 DERP Relay：

```go
// Hub 已有的能力
func (s *Server) handlePeerWS(w, r) {
    // 握手 → 建立 WebSocket
    // 注册 inbound write channel
    // 所有消息经过 HandleMessage
    // 如果 TargetNodeID != localID → 盲转发
}

// 新增的 DERP 功能
func (s *Server) handleDERP(w, r) {
    // 与 peer/ws 类似，但：
    // - 不需要 trust store（短暂连接）
    // - 只做流量转发
    // - 有带宽/连接数限制
    // - E2E 加密（见 Phase 4）
}
```

---

## 8. 盲中继模型：transit 信任模式

**目标**：允许陌生人将你的 Hub 用做中继节点，同时 Hub 不暴露任何本地能力。
**投入**：~3 天（不依赖 Phase 1-5，可独立实现）

### 8.1 概念

当前的 `TrustPolicy.Mode` 只有 `"full"`（完全信任）。新增 `"transit"` 模式：

```go
type TrustPolicy struct {
    Mode string `json:"mode"` // "full" | "transit"
}
```

| 权限 | full | transit |
|------|------|---------|
| 连接 Hub | ✅ | ✅ |
| 通过 Hub 转发消息给其他节点 | ✅ | ✅ |
| 请求 Hub 的本地能力（`fs.read` 等） | ✅ | ❌ |
| 请求 Hub 自身的配置/状态 | ✅ | ❌ |
| E2E 加密通信 | 可选 | 强制 |
| 是否存储在 TrustStore | ✅ | 可选 |

### 8.2 实现

**拓扑层拦截**（`topology.go:HandleMessage`）：

```go
func (pt *PeerTopology) HandleMessage(senderID types.NodeID, data []byte) {
    msg, _ := protocol.UnmarshalMessage(data)

    // ==== 新增：transit 检查 ====
    if pt.peerPolicy[senderID] == "transit" {
        // transit 节点只能转发，不能请求本地能力
        targetIsLocal := msg.TargetNodeID == "" || msg.TargetNodeID == pt.localID
        if targetIsLocal {
            // 拒绝 transit 节点的本地请求
            pt.sendError(senderID, msg.RequestID, "TRANSIT_MODE_REJECTED",
                "this node is in transit mode and may not request local capabilities")
            return
        }
        // 非本地请求 → 允许转发（盲转，不检查内容）
        pt.forwardBlind(msg.TargetNodeID, data)
        return
    }
    // ==== 结束 ====

    // 原有逻辑继续...
    switch msg.Type {
    case protocol.MsgTypeMeshCall:
        pt.handleMeshCall(senderID, msg)
    // ...
    }
}
```

### 8.3 速率限制

`transit` 节点应有带宽和连接数限制：

```go
type TransitPolicy struct {
    MaxConnections    int   `json:"maxConnections"`    // 默认 5
    MaxBandwidthBps   int64 `json:"maxBandwidthBps"`   // 默认 1Mbps
    MaxDailyBytes     int64 `json:"maxDailyBytes"`     // 默认 500MB
    IdleTimeoutSecs   int   `json:"idleTimeoutSecs"`   // 默认 300
}
```

### 8.4 transit 接入方式

```
Option A: 公开邀请码
  用户生成 transit 邀请码 → 陌生人使用这个邀请码配对
  → 配对后 TrustPolicy.Mode = "transit"

Option B: 开放注册
  Hub 提供一个 /peer/register 端点
  任何节点 POST 自己的 ed25519 公钥
  → Hub 存储为 transit 节点
  → 返回 Hub 的公钥指纹用于验证

Option C: 预共享密钥
  Hub 公布一个 PSK（如 "mypublicrelay2024"）
  陌生节点握手时出示 PSK
  → 验证通过后赋予 transit 权限
```

---

## 9. TLS 证书与浏览器 PWA 支持

### 9.1 问题

- Edge/Chrome 需要 HTTPS 才能安装 PWA
- `localhost` 和 `127.0.0.1` 被浏览器视为"安全上下文"——不需要证书
- 但通过局域网 IP 或公网访问时，无 HTTPS 则 PWA 不可用

### 9.2 方案 A：localhost 管理（零配置）

```go
// Admin UI 绑定到 localhost：PWA 无需证书可工作
func (s *Server) startAdminServer() {
    adminMux := http.NewServeMux()
    // 管理 API + 前端 SPA
    adminAddr := "127.0.0.1:9190"
    http.ListenAndServe(adminAddr, adminMux)
}
```

Chrome/Edge 对 `127.0.0.1` 和 `localhost` 的 PWA 支持不需要任何证书。
适合"本机管理"场景。

### 9.3 方案 B：内建自签名 CA（推荐）

启动时自动生成 CA 并签发 TLS 证书，代码完成，无需 OpenSSL：

```go
package crypto

import (
    "crypto/ecdsa"
    "crypto/elliptic"
    "crypto/rand"
    "crypto/x509"
    "crypto/x509/pkix"
    "math/big"
    "net"
    "time"
)

// GenerateInternalTLS 生成自签名 CA 并用其签发服务器证书。
// cert 用于 tls.Config.Certificates
// caPool 用于 tls.Config.RootCAs（客户端验证时使用）
// caPEM 用于输出给用户手动安装到浏览器
func GenerateInternalTLS(hosts []string) (
    cert tls.Certificate,
    caPool *x509.CertPool,
    caPEM []byte,
    err error,
) {
    // 1. 生成 CA 密钥对 + 自签名 CA 证书
    caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
    caTemplate := &x509.Certificate{
        SerialNumber:          big.NewInt(1),
        IsCA:                  true,
        BasicConstraintsValid: true,
        NotBefore:             time.Now().Add(-1 * time.Hour),
        NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 年
        Subject:               pkix.Name{CommonName: "SessionBridge Internal CA"},
        KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
    }
    caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)

    // 2. 用 CA 签发服务器证书
    serverKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
    serverTemplate := &x509.Certificate{
        SerialNumber: big.NewInt(2),
        NotBefore:    time.Now().Add(-1 * time.Hour),
        NotAfter:     time.Now().Add(1 * 365 * 24 * time.Hour),
        Subject:      pkix.Name{CommonName: "SessionBridge Node"},
        KeyUsage:     x509.KeyUsageDigitalSignature,
        ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
    }
    for _, h := range hosts {
        if ip := net.ParseIP(h); ip != nil {
            serverTemplate.IPAddresses = append(serverTemplate.IPAddresses, ip)
        } else {
            serverTemplate.DNSNames = append(serverTemplate.DNSNames, h)
        }
    }
    serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)

    // 3. 组装
    cert = tls.Certificate{
        Certificate: [][]byte{serverDER, caDER},
        PrivateKey:  serverKey,
    }
    caPool = x509.NewCertPool()
    caPool.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
    caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

    return
}
```

**用户侧体验**：

```bash
# 首次启动输出：
==== SessionBridge Internal CA Certificate ====
安装此证书到浏览器以启用 HTTPS + PWA：
1. 复制下方 PEM 块
2. Edge → 设置 → 管理证书 → 受信任的根证书颁发机构 → 导入
3. Chrome → 设置 → 隐私和安全 → 安全 → 管理证书 → 导入
────
-----BEGIN CERTIFICATE-----
MIIB...
-----END CERTIFICATE-----
============================================

# 第二次启动及以后：
# → 使用持久化到磁盘的 CA，证书自动续签
# → 已有 CA 的用户不受影响
```

### 9.4 方案 C：ACME Let's Encrypt（有域名时）

```go
import "golang.org/x/crypto/acme/autocert"

m := &autocert.Manager{
    Prompt:     autocert.AcceptTOS,
    HostPolicy: autocert.HostWhitelist("myhub.example.com"),
    Cache:      autocert.DirCache("~/.sessionnode/certs"),
}

srv := &http.Server{
    Addr:      ":443",
    TLSConfig: &tls.Config{GetCertificate: m.GetCertificate},
}
```

前提：公网域名 + 端口 80/443 可达。适合有域名的部署场景。

### 9.5 推荐策略

| 访问方式 | 方案 | 证书需求 |
|----------|------|----------|
| 本机管理 (`127.0.0.1`) | localhost | ❌ 不需要，PWA 可工作 |
| 内网访问 (`192.168.x.x`) | 自签名 CA | 需安装一次 CA 证书到浏览器 |
| 公网中继 (有域名) | Let's Encrypt | 自动申请 |
| 公网中继 (无域名) | 自签名 CA | 需安装 CA |

**核心判断**：TLS 仅为浏览器/ PWA 服务。Go 节点间的通信由消息加密（Phase 1/2/4）提供保密性，TLS 不是必须的。

---

## 10. Relay-CA 模型：自签发节点证书

**目标**：不依赖 x.509 PKI，用 ed25519 签名实现"官方证书"的分发和验证。
**状态**：✅ 已完成

**投入**：~2 天
**依赖**：无（基于现有 ed25519 身份）

### 10.1 为什么需要

当前 invite 配对模型要求每对节点之间直接交换公钥。Relay-CA 模型允许：

```
场景：NodeX 是陌生人，NodeY 是你信任的设备

NodeX ←─ 持有 Relay-CA 签发的证书
NodeY ←─ 信任 Relay-CA 的公钥

NodeY 不直接认识 NodeX，但：
  NodeY 验证 NodeX 证书上的 Relay-CA 签名 → 信任 NodeX
  不需要 NodeX ↔ NodeY 直接配对
```

### 10.2 证书格式

```go
type NodeCertificate struct {
    NodeID      string `json:"nodeId"`      // 被签发节点的 ID
    PublicKey   []byte `json:"publicKey"`   // 被签发节点的 ed25519 公钥
    IssuedBy    string `json:"issuedBy"`    // Relay-CA 的 NodeID
    IssuedAt    int64  `json:"issuedAt"`
    ExpiresAt   int64  `json:"expiresAt"`
    Mode        string `json:"mode"`        // "full" | "transit"
    Signature   []byte `json:"signature"`   // Relay 的 ed25519 签名
}

// 签发
func (ca *NodeIdentity) IssueCertificate(target *NodeIdentity, mode string, ttl time.Duration) *NodeCertificate {
    cert := &NodeCertificate{
        NodeID:    target.NodeID,
        PublicKey: target.PublicKey,
        IssuedBy:  ca.NodeID,
        IssuedAt:  time.Now().UnixMilli(),
        ExpiresAt: time.Now().Add(ttl).UnixMilli(),
        Mode:      mode,
    }
    // 签名内容：NodeID + PublicKey + ExpiresAt + Mode
    msg := cert.serializedForSigning()
    sig, _ := ca.Sign(msg)
    cert.Signature = sig
    return cert
}

// 验证
func VerifyCertificate(cert *NodeCertificate, caPubKey ed25519.PublicKey) bool {
    msg := cert.serializedForSigning()
    return ed25519.Verify(caPubKey, msg, cert.Signature)
}
```

### 10.3 使用流程

```
场景：你（Relay 运营者）提供一个公开中继，允许任何人注册使用

1. 你的 Hub 生成 ed25519 CA 密钥对（就是 Hub 自己的身份）
2. Hub 公开 CA 公钥指纹（在你的网站上、README 里）
3. 陌生人 NodeX：
   a. 生成自己的 ed25519 身份
   b. 通过某种方式向 Hub 注册（API / Web UI / 一次性码）
   c. Hub 验证注册请求（可能有人工审核或自动处理）
   d. Hub 为 NodeX 签发 transit 证书
   e. NodeX 拿到证书
4. NodeX 连接其他信任 Relay-CA 的节点时：
   出示证书 → 对方用已知的 Relay-CA 公钥验证 → 信任建立
```

### 10.4 与现有邀请码系统的关系

```
邀请码配对（现有）                Relay-CA 签发（新增）
  peer-to-peer                     hub-to-node
  双向交换公钥                      单向签发
  适用于自己的多台设备               适用于陌生人接入
  不需要第三方中介                   需要 Relay 作为中介
  永久或可配有效期                   带有有效期，可自动吊销
```

两者可以共存。对于你的个人设备，仍然用 invite 配对（full trust）。
对于愿意使用你的中继的陌生人，走 relay-CA（transit trust）。

---

## 11. 对已有实现的冲突分析

### Phase 1（消息级 AES-256-GCM）— 冲突低

| 模块 | 冲突点 | 改造难度 |
|------|--------|----------|
| `pkg/protocol/message.go` | 加 `EncryptedPayload`、`EncryptNonce`、`KeyID` 三个字段，不破坏现有结构 | ✅ ~5 行 |
| `internal/crypto/`（新建） | 新包，不影响现有模块 | ✅ 零冲突 |
| `internal/server/server.go:handlePeerWS` | 握手后（line 485-528）插入 ECDH + 注册 cipher | ✅ ~20 行 |
| `internal/topology/topology.go:peerHandshake` | 握手后插入 ECDH | ✅ ~10 行 |
| `internal/topology/topology.go:forward` | marshal 前加密 payload | ✅ ~5 行 |
| `internal/topology/topology.go:HandleMessage` | unmarshal 后解密 payload | ✅ ~5 行 |
| `internal/executor/*` | **不感知加密**——解密发生在拓扑层，executor 看到的已是明文 | ✅ 零冲突 |
| `internal/server/server.go:writeLoop` | **不修改**——加密在数据入 channel 前已完成 | ✅ 零冲突 |

**向后兼容**：握手消息携带 `caps: ["encrypt-v1"]`，仅在双方都支持时启用加密。
旧节点连接时自动降级为明文。

### Phase 2（Noise 协议替换握手）— 冲突中

| 模块 | 冲突点 | 改造难度 |
|------|--------|----------|
| `server.go:handlePeerWS` | 替换 lines 342-485 的 6 步握手为 Noise XX 的 3 步 | ⚠️ 需完整替换 |
| `topology.go:peerHandshake` | 替换 lines 788-873 | ⚠️ 需完整替换 |
| `topology.go:connectLoop` | 只改传参，不改进程 | ✅ |
| 握手后加密 | **不再需要** Phase 1 的独立 ECDH + AES-GCM——Noise 内建加密 | 简化 |

**兼容性**：握手第一步带版本号 `v: "1"`（ed25519）/ `v: "2"`（noise）。
新版节点同时支持两者，旧版不受影响。过渡期两个大版本。

### Phase 4（E2E 中继盲加密）— 冲突较高，但有正向依赖

| 模块 | 冲突点 |
|------|--------|
| `topology.go:HandleMessage` | 需要区分"本节点解密"和"盲转发"两条路径 |
| `topology.go:forward` | 需要 E2E 加密（用目标节点公钥），而非仅用对端会话密钥 |
| `dispatcher.go:Dispatch` | Hub 不再对 E2E 加密消息执行 dispatch（Forward-Only Mode 正好需要） |

**关键依赖关系**：Phase 4 和 **改动 1（Forward-Only Hub Mode）** 是正向强依赖。

```
Forward-Only Hub + E2E 加密 = 天生一对
  - Hub 不执行任何能力请求 ← 改动 1
  - Hub 也读不了请求内容  ← Phase 4
  → Hub 退化为纯字节管道
```

### 冲突汇总

| 新功能 | 对现有代码影响 | 兼容性策略 |
|--------|--------------|-----------|
| Phase 1（消息加密） | 低（+字段，+加密层） | 握手能力协商，自动降级 |
| Phase 2（Noise 握手） | 中（替换握手） | 版本协商 `v1`/`v2` |
| Phase 3（密钥轮换） | 低（+轮换协议消息） | 旧节点忽略轮换请求 |
| Phase 4（E2E 盲加密） | 较高（+转发路径改造） | 依赖改动 1 |
| Phase 5（NAT/中继） | 中（+STUN/ICE/中继端） | 独立模块 |
| transit 模式 | 低（+ Policy mode） | 不影响现有 full 模式 |
| 自签名 TLS | 低（+可选 tls.Config） | 不影响现有 TLS 配置 |
| Relay-CA 签发 | 低（+ NodeCertificate） | 不替代现有 invite 配对 |

---

## 12. 推荐实施路径

### 演进路线图

```
优先级        短期                   中期                     长期
   │            │                     │                       │
   │  当前 ←────┤                     │                       │
   │            ▼                     │                       │
   │  立即实施(1) 消息 AES-256-GCM    │                       │
   │            │ ← 最大安全收益      │                       │
   │            │ ← 最小代码改动      │                       │
   │            │ ← 无外部依赖        │                       │
   │            ▼                     │                       │
   │  并行实施(2) transit 信任模式    │                       │
   │            │ ← 陌生人接入中继    │                       │
   │            ▼                     │                       │
   │  并行实施(3) 自签名 CA TLS       │                       │
   │            │ ← PWA 支持          │                       │
   │            ▼                     ▼                       │
   │          ▸ 改动 1 (Forward-Only) ← 必须先于 Phase 4     │
   │            │                     │                       │
   │            │                     ▼                       │
   │            │            Phase 2 (Noise 协议)             │
   │            │                     │                       │
   │            │                     ▼                       │
   │            │            Phase 3 (密钥轮换)               │
   │            │                     │                       │
   │            │                     ▼                       │
   │            ▼            Phase 4 (E2E 盲加密)             │
   │                     (依赖改动 1 + Phase 1/2)            │
   │                              │                           │
   │                              ▼                           │
   │                     Relay-CA 证书签发                    │
   │                              │                           │
   │                              ▼                           │
   │                     Phase 5 (NAT 穿透/中继)              │
   │                              │                           │
   │                              ▼                           │
   │                     目标：个人版 Tailscale                │
   │                     (公网盲中继 + 中继CA + P2P)          │
   ▼                              │                           │
```

### 实施顺序建议

| 顺序 | 改动 | 时间 | 独立？ |
|------|------|------|--------|
| 0 | Forward-Only Hub Mode（CORE_CHANGES 改动 1） | 1-2 天 | ✅ 独立 |
| 1 | **Phase 1：消息级加密** | 2 天 | ✅ 独立 |
| 2 | **transit 信任模式** | 2-3 天 | ✅ 独立 |
| 3 | **自签名 CA TLS** | 1 天 | ✅ 独立 |
| 4 | 改动 5：Admin 端点 | 1 天 | ✅ 独立 |
| 5 | **Phase 2：Noise 协议** | 1 周 | ✅ 独立 |
| 6 | **Phase 3：密钥轮换** | 2 天 | 需 Phase 2 |
| 7 | **Phase 4：E2E 盲加密** | 1 周 | 需改动 1 + Phase 1/2 |
| 8 | **Relay-CA 证书签发** | 2 天 | ✅ 独立（推荐 Phase 4 后）|
| 9 | **Phase 5：NAT 穿透** | 2-3 周 | 需 Phase 4 |

**最短可行路径（两周内上线）**：0 → 1 → 2 → 3 → 4

**完全体（三个月内）**：0 → 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9
