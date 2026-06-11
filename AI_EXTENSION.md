# SessionBridge Core — AI 电脑操作扩展分析

> 本文档分析将 SessionBridge Core 从"远程终端/开发工具"扩展到
> "AI 电脑操作"场景的能力缺口、实现方案和当前进展。

---

## 目录

1. [现状概览](#1-现状概览)
2. [能力覆盖度分析](#2-能力覆盖度分析)
3. [已实现的能力](#3-已实现的能力)
4. [待实现的能力](#4-待实现的能力)
5. [架构适配度](#5-架构适配度)
6. [实施路线](#6-实施路线)

---

## 1. 现状概览

### 当前注册能力数：**89 个**（持续增长中）

```go
// 按类归类（实际 registerCore + registerConditional）
session.*          × 5    // create, destroy, list, info, get
stream.*           × 5    // subscribe, write, list, replay, tail
fs.*               × 7    // read, write, list, mkdir, remove, rename, stat
process.*          × 4    // spawn, signal, resize, list
env.*              × 8    // get, set, list, unset, checkBinary, which, home, cwd
config.*           × 4    // get, set, list, reset
notify.*           × 3    // send, request, respond
approval.*         × 1    // list
system.*           × 1    // info
node.*             × 3    // list, info, health
node.peer.*        × 5    // list, info, reconnect, disconnect, revoke
node.identity.*    × 1    // get
node.invite.*      × 4    // create, list, revoke, accept
node.reachability  × 1    // check
session.history.*  × 6    // getPolicy, setPolicy, stats, list, clear.plan, clear.execute
logs.* / audit.*   × 3    // tail, query, list
task.*             × 2    // list, info
run.*              × 6    // create, list, info, stop, updatePolicy, attach
operations.*       × 6    // list, get, dryRun, rollback, rollbackRange, verify
update.*           × 8    // status, source.get/set, policy.get/set, check, plan, ignore
desktop.*          × 3    // clipboard.get, clipboard.set, screenshot  ← 新增
──────────────────────────────────────────
总计               89
```

### 对比修正

之前的数据源是过时的。以下是对比：

| 指标 | 旧数据 | 实际 |
|------|--------|------|
| 能力总数 | 58 | **89** |
| 通知系统 | "缺失" | ✅ 已有 `notify.*` x 3 |
| 注册模式 | "需重构" | 不需要，已有 `registerCore` + `registerConditional` 按需组合 |

---

## 2. 能力覆盖度分析

### "开发工具"场景覆盖

```
功能              覆盖度    对应能力
────────────────────────────────────────
文件读写           ✅      fs.read / fs.write / fs.list / fs.stat
进程管理           ✅      process.spawn / process.signal / process.list
Shell 执行         ✅      session.create + process.spawn
实时输出           ✅      stream.subscribe / stream.write / stream.tail
环境变量           ✅      env.get / env.set / env.list
配置管理           ✅      config.get / config.set / config.list
通知推送           ✅      notify.send
```

### "AI 电脑操作"场景覆盖

```
功能              覆盖度    对应能力              实现状态
────────────────────────────────────────────────────────
文件操作           ✅      fs.*                 已在
进程管理           ✅      process.*            已在
Shell 命令         ✅      session.* + spawn    已在
剪贴板操作         ✅      desktop.clipboard.*   ✅ 本文新增
屏幕截图           ✅      desktop.screenshot    ✅ 本文新增
────────────────────────────────────────────────────────
桌面 GUI 控制      ❌      desktop.click        未实现
桌面键盘输入       ❌      desktop.type         未实现
桌面鼠标操作       ❌      desktop.mouse        未实现
浏览器控制         ❌      browser.*            未实现
无障碍树读取       ❌      perception.acc       未实现
OCR                ❌      perception.ocr       未实现
```

---

## 3. 已实现的能力

### desktop.clipboard.get / desktop.clipboard.set

**文件**：`internal/executor/desktop_cmds.go`

| 平台 | 实现方式 | 外部依赖 |
|------|---------|---------|
| Windows | Win32 API (`OpenClipboard` / `GetClipboardData` / `SetClipboardData`) | ❌ 无 |
| macOS  | `pbpaste` / `pbcopy` 命令 | ❌ 无 |
| Linux  | `xclip` / `wl-paste` / `wl-copy` | 需要 xclip 或 wl-clipboard |

**示例请求**：
```json
{
  "type": "action.request",
  "capability": "desktop.clipboard.get",
  "requestId": "req-001"
}
```

**示例响应**：
```json
{
  "type": "action.response",
  "ok": true,
  "requestId": "req-001",
  "payload": {"text": "剪贴板内容", "found": true}
}
```

### desktop.screenshot

**文件**：`internal/executor/desktop_cmds.go`

| 平台 | 实现方式 | 外部依赖 |
|------|---------|---------|
| Windows | GDI (`CreateDC` + `BitBlt` + `GetDIBits`) | ❌ 无 |
| macOS  | `screencapture -x` 命令 | ❌ 无 |
| Linux  | `import` (ImageMagick) / `gnome-screenshot` | 需要 ImageMagick 或 gnome-screenshot |

**示例请求**：
```json
{
  "type": "action.request",
  "capability": "desktop.screenshot",
  "payload": {"monitorIndex": 0}
}
```

**示例响应**（Payload 约数百 KB base64）：
```json
{
  "type": "action.response",
  "ok": true,
  "payload": {
    "data": "data:image/png;base64,iVBORw0KGgo...",
    "width": 1920,
    "height": 1080,
    "format": "png"
  }
}
```

> **注意**：大图场景下（2560×1440 及以上），base64 编码后的响应体可能超过 10MB。
> 可优化方向：返回一个临时文件 URL，由客户端通过 HTTP GET 拉取。

---

## 4. 待实现的能力

按实现难度排序：

### 第一档：轻代价（各 ~1-2 天）

| 能力 | 实现方案 | 外部依赖 |
|------|---------|---------|
| `input.keyboard` | Windows: `SendInput` API；macOS: `CGEventPost`；Linux: `/dev/uinput` / `xdotool` | ❌ 无（syscall）|
| `input.mouse` | 同上 | ❌ 无 |
| `desktop.click` | 组合 mouse.move → mouse.click 高层操作 | ❌ 无 |
| `desktop.type` | 组合 keyboard 逐键发送 | ❌ 无 |

### 第二档：中代价（各 ~1 周）

| 能力 | 实现方案 | 外部依赖 |
|------|---------|---------|
| `browser.navigate` | Chrome DevTools Protocol (CDP) over WebSocket | ❌ 无（标准库 net/http）|
| `browser.click` | CDP `Input.dispatchMouseEvent` | ❌ 无 |
| `browser.screenshot` | CDP `Page.captureScreenshot` | ❌ 无 |
| `browser.eval` | CDP `Runtime.evaluate` | ❌ 无 |

### 第三档：重代价（2-4 周）

| 能力 | 难点 |
|------|------|
| `perception.accessibility` | Windows UIA 是 COM 接口（Go 调用困难）；macOS Accessibility API 需要 CGo；Linux AT-SPI 通过 D-Bus |
| `perception.ocr` | Core 不应直接做 OCR——Core 提供截图，由 AI 层调用 OCR 服务 |

### 架构建议

浏览器自动化建议用 CDP（Chrome DevTools Protocol），而不是市面上常见的 Puppeteer/Playwright 封装。原因是：

```
Puppeteer/Playwright 方案：   CDP 直接方案：
  安装 Chromium (300MB)        连接已有 Chrome/Edge
  npm install (200MB)         直接用 WebSocket 发 CDP 命令
  Node.js 运行                Go 的 gorilla/websocket 即可
  额外进程管理                 同进程管理
```

对于用户已有 Chrome/Edge 的场景，CDP 直连模式（`--remote-debugging-port`）最轻量。

---

## 5. 架构适配度

### ✅ 不需要重构

当前 `action.request / action.response` 模式天然匹配 AI tool_use 模型：

| AI tool_use 概念 | SessionBridge Core 对应 |
|------------------|------------------------|
| tool 声明 | `r.Register("desktop.click", handler)` |
| tool 调用 | `action.request { capability: "desktop.click" }` |
| tool 响应 | `action.response { ok: true, payload: {...} }` |
| 工具列表 | `capability/known.go` + `AllPluginsCaps` |
| 平台限制 | `capability/support.go` Matrix |
| 权限控制 | `permission/checker.go` (可对特定工具设 allow/deny/ask) |

### ✅ 注册模式已验证扩展性

从最初的 ~60 个能力扩展到现在的 89 个，注册模式（`registerCore` + `registerConditional`）和目录结构（按类别分文件）被证明是可持续的。

### ⚠️ 注意点

1. **大负载响应**：screenshot 返回 base64 可能数 MB。当前 WebSocket JSON 编码会膨胀 ~33%。建议未来支持 binary frame 通道。
2. **有状态操作**：浏览器会话需要 session 关联。可复用现有的 `session.create / session.destroy` 模式。
3. **跨平台屏幕权限**：
   - macOS：需要 Screen Recording 权限（`System Preferences → Security & Privacy → Screen Recording`）
   - Linux Wayland：需要 portal 授权（`xdg-desktop-portal`）
   - Windows：GDI 截图不需要额外权限

---

## 6. 实施路线

### 短期（1-2 周）✅ 本文已完成

| 能力 | 状态 | 文件 |
|------|------|------|
| `desktop.clipboard.get` | ✅ 已实现 | `internal/executor/desktop_cmds.go` |
| `desktop.clipboard.set` | ✅ 已实现 | 同上 |
| `desktop.screenshot` | ✅ 已实现 | 同上 |
| `desktop.click` | ⏳ 待实现 | 可加在 `desktop_cmds.go` |

### 中期（2-4 周）

| 能力 | 优先级 | 备注 |
|------|--------|------|
| `browser.*` | 高 | CDP 协议，适合控制浏览器 |
| `input.keyboard/mouse` | 高 | 依赖 syscall，跨平台 |
| `desktop.scroll` | 中 | 鼠标滚轮转发 |

### 长期（1-2 月）

| 能力 | 优先级 | 备注 |
|------|--------|------|
| `perception.accessibility` | 中 | 无障碍树读取，作为视觉模型的补充 |
| `perception.ocr` | 低 | 借助外部 AI 服务 |

---

## 关联文档

- [CORE_CHANGES.md](CORE_CHANGES.md) — 整体改造规格书
- [ENCRYPTION.md](ENCRYPTION.md) — 加密架构与盲中继方案
- [DEPLOY.md](DEPLOY.md) — 零配置部署能力
