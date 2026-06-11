# SessionBridge Core — 零配置部署能力（单点能力）

> 本文档定义三个独立的核心能力，用于实现"开端口给 IP，自动部署全套环境"的目标。
> 三个能力互相独立，互不依赖，各自可独立实现。

---

## 目录

1. [能力 A：Release 自更新](#能力-arelease-自更新)
2. [能力 B：中继自动注册](#能力-b中继自动注册)
3. [能力 C：mDNS LAN 自动发现](#能力-cmdns-lan-自动发现)

---

## 能力 A：Release 自更新

**目标**：让 Core 能够从 GitHub Releases 检查并应用更新，不依赖 `git` 命令或 `.git` 目录。
**文件影响**：`internal/update/` 下的 3 个文件修改 + 1 个新建
**冲突**：无（纯增量，不改动现有 git source 路径）
**Go 依赖**：无（标准库 `net/http` + `encoding/json`）
**编译注入**：需要 `ldflags` 注入当前版本号

### A.1 更新源扩展

```go
// internal/update/source.go（现有 update.go，新增 UpdateSourceType）

type SourceType string

const (
    SourceTypeGit     SourceType = "git"     // 已有：依赖 git 命令 + .git 目录
    SourceTypeRelease SourceType = "release" // 新增：从 GitHub Releases 检查更新
)

// UpdateSource 扩展
type UpdateSource struct {
    Type SourceType `json:"type"` // "git" | "release"

    // ==== Git 源（已有） ====
    Remote  string `json:"remote,omitempty"`
    Branch  string `json:"branch,omitempty"`
    RepoURL string `json:"repoUrl,omitempty"`

    // ==== Release 源（新增） ====
    ReleaseRepo     string `json:"releaseRepo,omitempty"` // "PENG1028/sessionbridge-core"
    ReleaseCurrent  string `json:"releaseCurrent,omitempty"` // 当前版本号，编译时注入
    ReleaseAsset    string `json:"releaseAsset,omitempty"`   // 目标平台 asset 名模式
    // 示例: "sessionbridge-core_{{ .Version }}_{{ .Os }}_{{ .Arch }}.tar.gz"
}
```

### A.2 版本号注入

```go
// cmd/node/main.go 新增编译时注入

// 在 package main 中：
var (
    Version   = "dev"         // -X main.Version=v1.0.0
    Commit    = "none"        // -X main.Commit=abc123
    BuildTime = "unknown"     // -X main.BuildTime=2024-01-01T00:00:00Z
)

// .goreleaser.yml 注入
// ldflags:
//   - -s -w
//   - -X main.Version={{ .Version }}
//   - -X main.Commit={{ .ShortCommit }}
//   - -X main.BuildTime={{ .Date }}
```

### A.3 Release 更新检查器

```go
// internal/update/release_checker.go — 新建

package update

import (
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
    "time"
)

// GitHubRelease 是 GitHub Releases API 的响应片段。
type GitHubRelease struct {
    TagName string `json:"tag_name"`
    Name    string `json:"name"`
    Draft   bool   `json:"draft"`
    Prerelease bool `json:"prerelease"`
    Assets  []struct {
        Name               string `json:"name"`
        BrowserDownloadURL string `json:"browser_download_url"`
        Size               int64  `json:"size"`
    } `json:"assets"`
}

// ReleaseChecker 从 GitHub Releases 检查更新。
type ReleaseChecker struct {
    Repo       string // "PENG1028/sessionbridge-core"
    CurrentTag string // "v1.0.0"
    HTTPClient *http.Client
}

// Check 查询最新 release 并与当前版本比较。
// 返回 (available, latestTag, downloadURL, error)
func (c *ReleaseChecker) Check() (bool, string, string, error) {
    url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.Repo)

    req, _ := http.NewRequest("GET", url, nil)
    req.Header.Set("Accept", "application/json")
    // User-Agent 是 GitHub API 要求的
    req.Header.Set("User-Agent", "sessionbridge-core-update-checker/1.0")

    resp, err := c.HTTPClient.Do(req)
    if err != nil {
        return false, "", "", fmt.Errorf("github api: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode == 403 {
        return false, "", "", fmt.Errorf("github api rate limited (HTTP 403)")
    }
    if resp.StatusCode != 200 {
        return false, "", "", fmt.Errorf("github api: HTTP %d", resp.StatusCode)
    }

    var release GitHubRelease
    if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
        return false, "", "", fmt.Errorf("github api decode: %w", err)
    }

    // 跳过 draft
    if release.Draft {
        return false, "", "", nil
    }

    // 比较版本号
    if release.TagName == c.CurrentTag || release.TagName == "" {
        return false, "", "", nil
    }

    // 找到当前平台的 asset
    assetName := c.assetName(release.TagName)
    for _, a := range release.Assets {
        if a.Name == assetName {
            return true, release.TagName, a.BrowserDownloadURL, nil
        }
    }

    // 没找到匹配当前平台的 asset
    return true, release.TagName, "", fmt.Errorf("no asset found for current platform (expected: %s)", assetName)
}

// assetName 构建平台相关的 asset 文件名。
func (c *ReleaseChecker) assetName(tag string) string {
    // 模式: sessionbridge-core_{{ .Tag }}_{{ .Os }}_{{ .Arch }}.tar.gz
    // 与 goreleaser.yml 的 name_template 保持一致
    name := fmt.Sprintf("sessionbridge-core_%s_%s_%s.tar.gz",
        tag, runtimeOS(), runtimeArch())
    return name
}

func runtimeOS() string {
    // runtime.GOOS 映射到 goreleaser 命名
    // "windows" → "windows", "linux" → "linux", "darwin" → "darwin"
    return runtime.GOOS
}

func runtimeArch() string {
    // runtime.GOARCH 映射
    // "amd64" → "amd64", "arm64" → "arm64"
    return runtime.GOARCH
}
```

### A.4 更新应用器

```go
// internal/update/apply.go — 新建

package update

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "runtime"
)

// ApplyOptions 控制更新行为。
type ApplyOptions struct {
    DownloadURL   string // asset 下载 URL
    ExpectedHash  string // SHA-256 校验和（可选，从 checksums.txt 获取）
    CurrentExe    string // 当前二进制路径（os.Executable()）
    ReplaceTarget string // 替换目标路径（通常 == CurrentExe）
    RestartCmd    []string // 重启命令（空 = 不自动重启）
}

// Apply 执行二进制替换更新。
//
// 流程：
//  1. 下载新二进制到临时文件
//  2. 校验 SHA-256（如果提供了 ExpectedHash）
//  3. 将新二进制写入同目录的 .new 文件
//  4. 替换当前二进制（平台相关）
//  5. 可选：执行重启命令
//
// 错误处理：任何步骤失败都回滚，不破坏当前二进制。
func Apply(opts ApplyOptions) error {
    // 步骤 1：下载
    tmpFile, err := os.CreateTemp("", "sessionbridge-update-*.tar.gz")
    if err != nil {
        return fmt.Errorf("create temp file: %w", err)
    }
    defer os.Remove(tmpFile.Name())

    resp, err := http.Get(opts.DownloadURL)
    if err != nil {
        return fmt.Errorf("download: %w", err)
    }
    defer resp.Body.Close()

    hash := sha256.New()
    writer := io.MultiWriter(tmpFile, hash)

    if _, err := io.Copy(writer, resp.Body); err != nil {
        tmpFile.Close()
        return fmt.Errorf("download write: %w", err)
    }
    tmpFile.Close()

    // 步骤 2：校验
    if opts.ExpectedHash != "" {
        got := hex.EncodeToString(hash.Sum(nil))
        if got != opts.ExpectedHash {
            return fmt.Errorf("checksum mismatch: got %s, expected %s", got, opts.ExpectedHash)
        }
    }

    // 步骤 3：解压 tar.gz → 提取二进制 → 写入 .new
    // （需要 compress/gzip + archive/tar 处理，此处省略具体解压逻辑）
    newPath := opts.ReplaceTarget + ".new"
    if err := extractBinary(tmpFile.Name(), newPath); err != nil {
        return fmt.Errorf("extract binary: %w", err)
    }

    // 步骤 4：替换
    if err := replaceBinary(opts.ReplaceTarget, newPath); err != nil {
        return fmt.Errorf("replace binary: %w", err)
    }

    return nil
}

// replaceBinary 用新文件替换当前二进制。
// 平台差异：
//   - Unix: rename(2) 原子替换
//   - Windows: 不能覆盖正在运行的 exe，需要 rename 或通过 .new 做 swap
func replaceBinary(target, newPath string) error {
    if runtime.GOOS == "windows" {
        return replaceBinaryWindows(target, newPath)
    }
    // Unix: 直接 rename
    if err := os.Rename(newPath, target); err != nil {
        return fmt.Errorf("rename %s → %s: %w", newPath, target, err)
    }
    if err := os.Chmod(target, 0755); err != nil {
        return fmt.Errorf("chmod: %w", err)
    }
    return nil
}

func replaceBinaryWindows(target, newPath string) error {
    // Windows 策略：将新二进制写为 .new，通过重启脚本完成替换
    // 或者：先 rename 当前 exe 为 .old，再 rename .new 为 .exe
    backup := target + ".old"
    os.Rename(target, backup) // 忽略错误，可能没有旧文件
    if err := os.Rename(newPath, target); err != nil {
        // 回滚：把 backup 恢复
        os.Rename(backup, target)
        return fmt.Errorf("windows rename: %w", err)
    }
    os.Remove(backup)
    return nil
}
```

### A.5 executor 注册

```go
// internal/executor/update_cmds.go — 注册 update.apply

func init() {
    // 在 registerDefaults 中注册（或单独的条件注册）
}

func updateApply(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
    if deps.UpdateManager == nil {
        return nil, &types.CoreError{Code: "NOT_AVAILABLE", Message: "update manager not configured"}
    }

    // 获取当前更新源
    src := deps.UpdateManager.Source()
    if src.Type != update.SourceTypeRelease {
        return nil, &types.CoreError{Code: "NOT_SUPPORTED",
            Message: "update.apply only supported for release-type sources"}
    }

    // 检查是否有可用更新
    status := deps.UpdateManager.Status()
    if status.Status != update.StatusUpdateAvail {
        return nil, &types.CoreError{Code: "NO_UPDATE",
            Message: "no update available, run update.check first"}
    }

    // 获取当前二进制路径
    exe, err := os.Executable()
    if err != nil {
        return nil, &types.CoreError{Code: "INTERNAL", Message: fmt.Sprintf("get executable: %v", err)}
    }

    // 执行更新
    err = update.Apply(update.ApplyOptions{
        DownloadURL:   status.RemoteCommit, // 在 release 模式下，RemoteCommit 存 download URL
        CurrentExe:    exe,
        ReplaceTarget: exe,
    })
    if err != nil {
        return nil, &types.CoreError{Code: "UPDATE_FAILED", Message: err.Error()}
    }

    // 返回成功，客户端应主动重启
    return map[string]interface{}{
        "status":         "applied",
        "requiresRestart": true,
        "message":        "update downloaded and applied, restart to activate",
    }, nil
}
```

### A.6 改动汇总

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/update/update.go` | 修改 | `UpdateSource` 加 `SourceType`、Release 字段 |
| `internal/update/release_checker.go` | **新建** | GitHub Releases API 检查器 |
| `internal/update/apply.go` | **新建** | 二进制下载、校验、替换 |
| `internal/executor/update_cmds.go` | 修改 | 条件注册 `update.apply` |
| `cmd/node/main.go` | 修改 | 加 `Version`、`Commit`、`BuildTime` 编译注入变量 |
| `.goreleaser.yml` | 修改 | `ldflags` 注入版本号 |

### A.7 向后兼容

- `SourceTypeGit` 路径完全不变
- `SourceTypeRelease` 不影响已有 `git` 源的用户
- Release 源在不支持 Release 路径的旧版 UI 上降级为显示 `"update source: release"`，不报错

---

## 能力 B：中继自动注册

**目标**：新节点只需知道中继地址，一键注册到中继并自动建立连接。
**文件影响**：`internal/server/` 下 1 个新文件 + `cmd/node/main.go` 修改
**冲突**：无（纯增量新端点，不改变现有 invite/pair 逻辑）
**Go 依赖**：无

### B.1 场景

```
用户买了一台 VPS，只在防火墙开了一个端口：

  用户： sessionnode --relay=hub.example.com:9090
        ↓
  二进制做的事：
    1. 生成身份（已有）
    2. 生成配置（已有）
    3. 向 hub.example.com:9090/peer/register 发送注册请求
    4. Hub 确认后，保存 Hub 为 TrustedPeer
    5. 自动连接 Hub
    6. 完成 → 节点已在线
```

### B.2 中继注册端点

```go
// internal/server/peer_register.go — 新建

package server

import (
    "crypto/ed25519"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "log"
    "net"
    "net/http"
    "time"

    "github.com/PENG1028/sessionbridge-core/internal/mesh"
    "github.com/PENG1028/sessionbridge-core/pkg/protocol"
)

// handlePeerRegister 处理新节点的注册请求。
//
// POST /peer/register
// {
//   "nodeId":       "abc...",
//   "publicKey":    "hex-encoded-ed25519-pubkey",
//   "fingerprint":  "sha256-of-pubkey",
//   "version":      "v1.0.0",
//   "mode":         "transit" | "full"    // 请求的信任模式
// }
//
// 响应：
// {
//   "status":       "registered",
//   "nodeId":       "hub-node-id",
//   "publicKey":    "hub-public-key-hex",
//   "fingerprint":  "hub-fingerprint",
//   "mode":         "transit",            // 实际分配的信任模式
//   "expiresAt":    1718000000000         // 信任到期时间（0=永久）
// }
//
// 验证策略（可配置）：
//   - open:    任何节点都可注册（公开中继）
//   - token:   需要携带预先共享的注册 token
//   - manual:  注册后需要管理员手动批准（返回 pending）
func (s *Server) handlePeerRegister(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
        return
    }

    if s.identity == nil || s.trustStore == nil {
        http.Error(w, `{"error":"server identity or trust store not configured"}`, http.StatusInternalServerError)
        return
    }

    var req struct {
        NodeID      string `json:"nodeId"`
        PublicKey   string `json:"publicKey"`
        Fingerprint string `json:"fingerprint"`
        Version     string `json:"version,omitempty"`
        Mode        string `json:"mode,omitempty"` // "transit" | "full"
        Token       string `json:"token,omitempty"` // 用于 token 验证模式
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
        return
    }

    // 验证必填字段
    if req.NodeID == "" || req.PublicKey == "" || req.Fingerprint == "" {
        http.Error(w, `{"error":"nodeId, publicKey, and fingerprint are required"}`, http.StatusBadRequest)
        return
    }

    // 拒绝自注册
    if req.NodeID == s.identity.NodeID {
        http.Error(w, `{"error":"cannot register yourself"}`, http.StatusBadRequest)
        return
    }

    // 验证公钥格式
    pubKeyBytes, err := hex.DecodeString(req.PublicKey)
    if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
        http.Error(w, `{"error":"invalid public key"}`, http.StatusBadRequest)
        return
    }

    // 根据验证策略决定信任模式
    mode := s.resolveRegisterMode(req)
    if mode == "" {
        http.Error(w, `{"error":"registration rejected by policy"}`, http.StatusForbidden)
        return
    }

    // 避免重复注册：如果已存在，更新而非覆盖
    existing, err := s.trustStore.Get(req.NodeID)
    if err == nil && existing != nil {
        // 已注册的节点：更新地址和状态，保留原有信任模式
        addresses := s.resolveRegisterAddresses(r)
        _ = s.trustStore.UpdatePeer(req.NodeID, func(p *mesh.TrustedPeer) {
            p.Addresses = addresses
            p.LastSeen = time.Now().UnixMilli()
            p.Status = mesh.TrustStatusOffline
        })
    } else {
        // 新节点：创建信任记录
        addresses := s.resolveRegisterAddresses(r)
        peer := &mesh.TrustedPeer{
            NodeID:         req.NodeID,
            Name:           req.NodeID,
            PublicKey:      pubKeyBytes,
            Fingerprint:    req.Fingerprint,
            Addresses:      addresses,
            AutoReconnect:  true,
            Status:         mesh.TrustStatusOffline,
            LastSeen:       time.Now().UnixMilli(),
            Policy:         mesh.TrustPolicy{Mode: mode},
        }
        if err := s.trustStore.Add(peer); err != nil {
            log.Printf("[register] failed to store peer %s: %v", req.NodeID, err)
            http.Error(w, `{"error":"failed to store peer"}`, http.StatusInternalServerError)
            return
        }
    }

    log.Printf("[register] peer %s registered (mode=%s)", req.NodeID, mode)

    // 返回 Hub 身份信息
    resp := map[string]interface{}{
        "status":      "registered",
        "mode":        mode,
        "nodeId":      s.identity.NodeID,
        "publicKey":   hex.EncodeToString(s.identity.PublicKey),
        "fingerprint": s.identity.Fingerprint,
        "peerWsPath":  "/peer/ws",
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}

// resolveRegisterMode 决定注册节点获得的信任模式。
func (s *Server) resolveRegisterMode(req struct {
    NodeID      string `json:"nodeId"`
    PublicKey   string `json:"publicKey"`
    Fingerprint string `json:"fingerprint"`
    Version     string `json:"version,omitempty"`
    Mode        string `json:"mode,omitempty"`
    Token       string `json:"token,omitempty"`
}) string {
    // 读取注册策略（从 config 或硬编码默认值）
    policy := s.registerPolicy  // 假设存在，从 config 加载

    switch policy {
    case "open":
        // 开放注册：所有人都 transit，除非请求 full 且满足条件
        if req.Mode == "full" {
            return "full" // 开放注册也允许 full（但要记录日志）
        }
        return "transit"

    case "token":
        // Token 验证：必须有正确的 token 才能注册
        if req.Token == "" || req.Token != s.registerToken {
            return ""
        }
        if req.Mode == "transit" {
            return "transit"
        }
        return "full"

    case "manual":
        // 手动审核：注册成功但状态为 pending
        // 需要在 handlePeerRegister 中返回 pending 状态
        // 管理员通过 admin API 批准后改为 full/transit
        return "pending"

    default:
        return "transit" // 默认 transit
    }
}

// resolveRegisterAddresses 从注册请求中解析节点地址。
func (s *Server) resolveRegisterAddresses(r *http.Request) []string {
    // 使用 TCP 连接中的远端地址
    host, _, err := net.SplitHostPort(r.RemoteAddr)
    if err != nil {
        return nil
    }
    if host == "" {
        return nil
    }
    // 假设对方监听在相同 IP 的默认端口
    return []string{host + ":9090"}
}
```

### B.3 配置扩展

```go
// internal/config/config.go — 新增字段

type CoreConfig struct {
    ListenAddr string     `json:"listenAddr"`
    TLS        TLSConfig  `json:"tls,omitempty"`
    DataDir    string     `json:"dataDir"`
    Auth       AuthConfig `json:"auth"`
    Log        LogConfig  `json:"log"`

    // ==== 新增：注册策略（Hub 端） ====
    RegisterPolicy string `json:"registerPolicy,omitempty"` // "open" | "token" | "manual"
    RegisterToken  string `json:"registerToken,omitempty"`  // token 验证模式时的 token
}
```

### B.4 客户端注册命令

```go
// cmd/node/main.go — 新增 --relay 和 --register 模式

func main() {
    // 解析 CLI 参数
    relayAddr := os.Getenv("SESSIONNODE_RELAY")
    registerMode := os.Getenv("SESSIONNODE_REGISTER") != "" // 是否启用注册

    // ... 现有初始化代码 ...

    // ==== 新增：如果指定了 relay 地址，自动注册 ====
    if relayAddr != "" {
        err := autoRegister(relayAddr, nodeIdentity, token)
        if err != nil {
            log.Printf("[register] auto-register to %s failed: %v (continuing)", relayAddr, err)
        } else {
            log.Printf("[register] successfully registered to relay %s", relayAddr)
            // 添加中继为 peer
            topo.AddOrUpdatePeer(relayNodeID, relayAddr, true)
        }
    }
    // =====================
}

// autoRegister 向中继发送注册请求并返回中继的身份信息。
func autoRegister(relayAddr string, identity *mesh.NodeIdentity, token string) error {
    // 构建注册 URL
    registerURL := fmt.Sprintf("http://%s/peer/register", relayAddr)

    // 准备请求体
    body := map[string]interface{}{
        "nodeId":      identity.NodeID,
        "publicKey":   hex.EncodeToString(identity.PublicKey),
        "fingerprint": identity.Fingerprint,
        "version":     Version,
        "mode":        "transit",
    }
    // 如果有令牌，携带令牌
    if token != "" {
        body["token"] = token
    }

    bodyBytes, _ := json.Marshal(body)

    client := &http.Client{Timeout: 15 * time.Second}
    resp, err := client.Post(registerURL, "application/json", bytes.NewReader(bodyBytes))
    if err != nil {
        return fmt.Errorf("register request: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        respBody, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("register rejected (HTTP %d): %s", resp.StatusCode, string(respBody))
    }

    var registerResp struct {
        Status      string `json:"status"`
        Mode        string `json:"mode"`
        NodeID      string `json:"nodeId"`
        PublicKey   string `json:"publicKey"`
        Fingerprint string `json:"fingerprint"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&registerResp); err != nil {
        return fmt.Errorf("decode response: %w", err)
    }

    if registerResp.Status != "registered" {
        return fmt.Errorf("unexpected status: %s", registerResp.Status)
    }

    // 将中继加入 TrustStore（可供后续连接使用）
    relayPubKey, _ := hex.DecodeString(registerResp.PublicKey)
    relayPeer := &mesh.TrustedPeer{
        NodeID:        registerResp.NodeID,
        Name:          registerResp.NodeID,
        PublicKey:     relayPubKey,
        Fingerprint:   registerResp.Fingerprint,
        Addresses:     []string{relayAddr},
        AutoReconnect: true,
        Status:        mesh.TrustStatusOffline,
        Policy:        mesh.TrustPolicy{Mode: registerResp.Mode},
    }

    // 写入 TrustStore（如果已存在则更新）
    if err := trustStore.Add(relayPeer); err != nil {
        // 可能已存在（重复注册），尝试更新
        trustStore.UpdatePeer(registerResp.NodeID, func(p *mesh.TrustedPeer) {
            p.Addresses = []string{relayAddr}
            p.AutoReconnect = true
        })
    }

    return nil
}
```

### B.5 服务端注册端点注册

```go
// internal/server/server.go — registerHandlers 新增

func (s *Server) registerHandlers() {
    mux := http.NewServeMux()
    mux.HandleFunc("/health", s.handleHealth)
    mux.HandleFunc("/ws", s.handleWS)
    mux.HandleFunc("/peer/ws", s.handlePeerWS)
    mux.HandleFunc("/peer/invite/accept", s.handlePeerInviteAccept)

    // ==== 新增：注册端点 ====
    mux.HandleFunc("/peer/register", s.handlePeerRegister)
    // ====================
}
```

### B.6 改动汇总

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/server/peer_register.go` | **新建** | `handlePeerRegister` 处理器 + 注册策略 |
| `internal/server/server.go` | 修改 | `registerHandlers` 增加 `/peer/register` 路由 |
| `internal/config/config.go` | 修改 | `CoreConfig` 加 `RegisterPolicy`、`RegisterToken` 字段 |
| `cmd/node/main.go` | 修改 | 新增 `SESSIONNODE_RELAY` env var + `autoRegister()` |

### B.7 安全考虑

- **开放注册**：任何知道 Hub 地址的人都可以注册。适合个人公开中继
- **token 验证**：携带 token 的人才能注册。适合半公开中继
- **手动审核**：注册后需管理员批准。适合企业内部中继
- **transit 默认**：即使是自动注册，默认也只给 transit 权限，不暴露本地能力

---

## 能力 C：mDNS LAN 自动发现

**目标**：同一局域网内的 SessionBridge 节点自动发现对方，无需手动配置地址。
**文件影响**：`internal/topology/` 下 1-2 个新文件
**冲突**：无（纯增量，不改变现有 peer 连接逻辑）
**Go 依赖**：`github.com/hashicorp/mdns`（纯 Go，零 CGo，~2K 行）

### C.1 设计

```
节点A (192.168.1.10)              节点B (192.168.1.20)
       │                               │
       │  mDNS 广播:                    │
       │  _sessionbridge._tcp.local     │
       │  TXT: fp=abc123...            │
       │───────────────────────────────→│
       │                               │
       │  mDNS 响应:                    │
       │  nodeB.local:9090             │
       │  TXT: fp=def456...            │
       │←───────────────────────────────│
       │                               │
       │  发现新节点 B                  │
       │  └─ K 在 TrustStore？          │
       │     ├─ 是 → 自动连接            │
       │     └─ 否 → 回调通知上层        │
```

### C.2 发现器实现

```go
// internal/topology/discovery.go — 新建

package topology

import (
    "context"
    "fmt"
    "log"
    "net"
    "strconv"
    "strings"
    "time"

    "github.com/hashicorp/mdns"

    "github.com/PENG1028/sessionbridge-core/pkg/types"
)

// DiscoveryConfig 控制 mDNS 发现行为。
type DiscoveryConfig struct {
    ServiceName string        // 服务类型，默认 "_sessionbridge._tcp"
    Port        int           // 本地服务端口，用于广播
    Interval    time.Duration // 广播间隔，默认 30s
    NodeID      types.NodeID
    Fingerprint string // 用于识别节点身份
}

// DiscoveredPeer 代表一个通过 mDNS 发现的节点。
type DiscoveredPeer struct {
    NodeID      types.NodeID
    Address     string   // "192.168.1.20:9090"
    Fingerprint string   // SHA-256 of public key
    Tags        []string // always includes "lan"
    Age         time.Duration
}

// DiscoveryCallback 在发现新节点时被调用。
type DiscoveryCallback func(peer DiscoveredPeer)

// Discoverer 管理 mDNS 服务的广播和发现。
type Discoverer struct {
    cfg      DiscoveryConfig
    server   *mdns.Server
    onFound  DiscoveryCallback
    known    map[string]time.Time // 已发现的节点 (key = nodeID)
    ctx      context.Context
    cancel   context.CancelFunc
}

// NewDiscoverer 创建并启动 mDNS 发现器。
// 同时做两件事：
//   1. 广播自己（让其他节点发现本机）
//   2. 监听其他节点（发现局域网中的其他 SessionBridge 节点）
func NewDiscoverer(cfg DiscoveryConfig) (*Discoverer, error) {
    if cfg.ServiceName == "" {
        cfg.ServiceName = "_sessionbridge._tcp"
    }
    if cfg.Interval == 0 {
        cfg.Interval = 30 * time.Second
    }

    d := &Discoverer{
        cfg:   cfg,
        known: make(map[string]time.Time),
    }

    ctx, cancel := context.WithCancel(context.Background())
    d.ctx = ctx
    d.cancel = cancel

    return d, nil
}

// Start 启动 mDNS 广播和监听。
func (d *Discoverer) Start() error {
    // 1. 配置本机服务信息用于广播
    host, _ := os.Hostname()
    info := []string{
        fmt.Sprintf("nodeId=%s", d.cfg.NodeID),
        fmt.Sprintf("fp=%s", d.cfg.Fingerprint),
        fmt.Sprintf("ver=%s", Version), // 编译注入版本
    }

    service, err := mdns.NewService(
        fmt.Sprintf("sessionbridge-%s", d.cfg.NodeID[:8]), // 实例名
        d.cfg.ServiceName,
        "",                    // 域名（空 = local）
        d.cfg.Port,           // 端口
        nil,                  // 本机 IP（nil = 自动检测）
        info,                 // TXT 记录
    )
    if err != nil {
        return fmt.Errorf("mdns new service: %w", err)
    }

    // 2. 启动服务器（同时完成广播 + 响应查询）
    server, err := mdns.NewServer(&mdns.Config{
        Zone: service,
    })
    if err != nil {
        return fmt.Errorf("mdns server: %w", err)
    }
    d.server = server

    // 3. 启动主动查询协程
    go d.queryLoop()

    return nil
}

// queryLoop 定期查询局域网内的其他 SessionBridge 节点。
func (d *Discoverer) queryLoop() {
    ticker := time.NewTicker(d.cfg.Interval)
    defer ticker.Stop()

    // 立即执行一次
    d.queryOnce()

    for {
        select {
        case <-ticker.C:
            d.queryOnce()
        case <-d.ctx.Done():
            return
        }
    }
}

// queryOnce 执行一次 mDNS 查询。
func (d *Discoverer) queryOnce() {
    entries := make(chan *mdns.ServiceEntry, 16)

    go func() {
        for entry := range entries {
            d.handleEntry(entry)
        }
    }()

    // 查询 _sessionbridge._tcp 服务
    mdns.Query(&mdns.QueryParam{
        Service:   d.cfg.ServiceName,
        Domain:    "local",
        Timeout:   time.Second * 5,
        Entries:   entries,
        WantUnicast: false, // LAN 广播
    })
}

// handleEntry 处理一个 mDNS 查询结果。
func (d *Discoverer) handleEntry(entry *mdns.ServiceEntry) {
    // 跳过自身
    if entry.Info != "" && strings.Contains(entry.Info, fmt.Sprintf("nodeId=%s", d.cfg.NodeID)) {
        return
    }

    // 解析 TXT 记录中的元数据
    var nodeID string
    var fingerprint string
    for _, field := range entry.InfoFields {
        if strings.HasPrefix(field, "nodeId=") {
            nodeID = strings.TrimPrefix(field, "nodeId=")
        }
        if strings.HasPrefix(field, "fp=") {
            fingerprint = strings.TrimPrefix(field, "fp=")
        }
    }

    if nodeID == "" {
        return
    }

    // 构造地址
    addr := ""
    if len(entry.AddrV4) > 0 {
        addr = net.JoinHostPort(entry.AddrV4.String(), strconv.Itoa(entry.Port))
    } else if len(entry.AddrV6) > 0 {
        addr = net.JoinHostPort(entry.AddrV6.String(), strconv.Itoa(entry.Port))
    }

    if addr == "" {
        return
    }

    peer := DiscoveredPeer{
        NodeID:      types.NodeID(nodeID),
        Address:     addr,
        Fingerprint: fingerprint,
        Tags:        []string{"lan"},
    }

    // 检查是否已经发现过
    d.mu.Lock()
    firstSeen, known := d.known[nodeID]
    if !known {
        d.known[nodeID] = time.Now()
    }
    d.mu.Unlock()

    if !known {
        log.Printf("[discovery] new peer found via mDNS: %s at %s (fp=%s)", nodeID, addr, fingerprint)
        if d.onFound != nil {
            d.onFound(peer)
        }
    }
}

// SetCallback 注册发现回调。
// 当发现新节点时调用，由拓扑层决定是否连接。
func (d *Discoverer) SetCallback(fn DiscoveryCallback) {
    d.onFound = fn
}

// Stop 停止 mDNS 服务。
func (d *Discoverer) Stop() {
    d.cancel()
    if d.server != nil {
        d.server.Shutdown()
    }
}
```

### C.3 集成到拓扑层

```go
// internal/topology/topology.go — PeerTopology 新增 mDNS 集成

type PeerTopology struct {
    // ... 现有字段 ...

    // ==== 新增 ====
    discoverer *Discoverer // mDNS 发现器，nil = 未启用
    // ==============
}

// New 中可选启动发现器
func New(cfg Config) *PeerTopology {
    pt := &PeerTopology{
        // ... 现有初始化 ...
    }

    // ==== 新增：如果配置了 LAN 发现 ====
    if cfg.EnableDiscovery {
        d, err := NewDiscoverer(DiscoveryConfig{
            Port:        extractPort(cfg.LocalAddr),  // 从监听地址解析端口
            NodeID:      cfg.LocalID,
            Fingerprint: cfg.Identity.Fingerprint,
        })
        if err == nil {
            d.SetCallback(func(peer DiscoveredPeer) {
                pt.onDiscoveredPeer(peer)
            })
            if err := d.Start(); err == nil {
                pt.discoverer = d
                log.Printf("[topology] mDNS discovery enabled on port %d", extractPort(cfg.LocalAddr))
            }
        }
    }
    // ===============

    return pt
}

// onDiscoveredPeer 在通过 mDNS 发现新节点时被调用。
func (pt *PeerTopology) onDiscoveredPeer(peer DiscoveredPeer) {
    // 检查是否已经在拓扑中
    pt.mu.RLock()
    existing, known := pt.peers[peer.NodeID]
    pt.mu.RUnlock()

    if known {
        // 已注册的 peer：如果地址变了就更新
        if existing.Address != peer.Address {
            pt.mu.Lock()
            pt.peers[peer.NodeID].Address = peer.Address
            pt.mu.Unlock()
            // 重新连接（新地址）
            pt.ReconnectPeer(peer.NodeID)
        }
        return
    }

    // 如果信任存储中有这个节点，自动添加并连接
    if pt.trustStore != nil {
        tp, err := pt.trustStore.Get(string(peer.NodeID))
        if err == nil && tp != nil && tp.Status != "revoked" && tp.Status != "expired" {
            log.Printf("[discovery] auto-connecting to known peer %s at %s", peer.NodeID, peer.Address)
            pt.AddOrUpdatePeer(peer.NodeID, peer.Address, true)
            return
        }
    }

    // 不在信任存储中的新节点：通过回调通知上层（如 UI 提示用户确认）
    log.Printf("[discovery] unknown peer %s at %s — not in trust store, ignoring", peer.NodeID, peer.Address)
}
```

### C.4 Config 扩展

```go
// internal/config/config.go — TopologyConfig 增加发现选项

type TopologyConfig struct {
    Peers []PeerConfig `json:"peers,omitempty"`

    // ==== 新增 ====
    EnableDiscovery bool `json:"enableDiscovery,omitempty"` // 启用 mDNS LAN 发现
    DiscoveryPort   int  `json:"discoveryPort,omitempty"`   // mDNS 端口（默认 = listenAddr 端口）
    // ==============
}
```

### C.5 go.mod 依赖

```
require (
    // ... 现有依赖 ...
    github.com/hashicorp/mdns v1.0.5
)
```

### C.6 改动汇总

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/topology/discovery.go` | **新建** | mDNS Discoverer 实现 |
| `internal/topology/topology.go` | 修改 | `PeerTopology` 增加 `discoverer` 字段；`New()` 可选启动；回调处理 |
| `internal/config/config.go` | 修改 | `TopologyConfig` 增加 `EnableDiscovery`、`DiscoveryPort` |
| `go.mod` | 修改 | 增加 `github.com/hashicorp/mdns` 依赖 |

### C.7 安全考虑

- mDNS 仅在 LAN 内广播，不跨子网
- 发现信息不包含私钥或敏感数据，只有 NodeID + 公钥指纹
- 发现后仍需信任存储验证才能自动连接
- 不受信任的节点不影响现有连接，只在日志中记录
- 可通过 `enableDiscovery: false` 完全关闭

---

## 三个能力独立实施说明

```
能力 A (Release 自更新)            能力 B (中继注册)             能力 C (mDNS 发现)
       │                                │                           │
       │ 修改:                           │ 修改:                      │ 修改:
       │ internal/update/*               │ internal/server/*          │ internal/topology/*
       │ internal/executor/*             │ internal/config/*          │ internal/config/*
       │ cmd/node/*                      │ cmd/node/*                 │ go.mod
       │ .goreleaser.yml                 │                           │
       │                                │                           │
       │ 仅影响 update.* 能力            │ 新增 HTTP 端点             │ 新增 mDNS 后台协程
       │ 不影响现有 git source           │ 不影响现有 invite 配对      │ 不影响现有 peer 连接
       │                                │                           │
       └────────── 三门独立技术债 ──────────────────────────┘
                   各自可独立实现，互不阻塞
                           │
                   三个合在一起 = "给个 IP:端口，自动部署"
```
