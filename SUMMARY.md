# lingma-ipc-proxy 项目总结

## 项目概述

**lingma-ipc-proxy** 是一个独立的 Go 后端服务，通过 Lingma 本地 pipe 或 WebSocket 传输与其通信，对外暴露兼容 OpenAI 和 Anthropic 的 API 接口。

### 核心功能

- **完整 API 适配**: 完整支持 OpenAI (`/v1/chat/completions`) 和 Anthropic (`/v1/messages`) 协议
- **流式与非流式响应**: 完整支持 SSE 流式输出和普通 JSON 响应
- **双传输层**: 支持 Windows Named Pipe 和 WebSocket 两种传输方式
- **直接 IPC 通信**: 直接与 Lingma 进程通信，不依赖 DOM/CDP
- **工具调用**: 完整支持 `tools` / `tool_choice`，兼容多轮 Agent 循环
- **多模态输入**: 支持图片输入（OpenAI `image_url` / Anthropic `image` source）
- **参数兼容**: 完整接收 `temperature`、`top_p`、`stop`、`presence_penalty` 等标准参数

---

## 架构设计

### 项目结构

```
lingma-ipc-proxy/
├── cmd/lingma-ipc-proxy/      # 入口程序
│   └── main.go                # 配置加载、服务启动、信号处理
├── internal/
│   ├── httpapi/               # HTTP API 层
│   │   ├── server.go          # HTTP 路由、请求处理、流式响应
│   │   └── server_test.go     # 工具模拟相关测试
│   ├── lingmaipc/             # IPC 通信层
│   │   ├── client.go          # JSON-RPC 客户端、通知订阅
│   │   ├── transport.go       # Pipe/WebSocket 传输实现
│   │   └── transport_test.go  # 传输层测试
│   ├── service/               # 业务逻辑层
│   │   └── service.go         # 会话管理、请求编排、模型列表
│   └── toolemulation/         # 工具调用模拟
│       └── toolemulation.go   # 工具定义解析、Action Block 处理
├── scripts/                   # 构建与服务安装脚本
│   ├── build.ps1
│   ├── install-nssm-service.ps1
│   └── install-winsw-service.ps1
├── config.example.json        # 配置文件示例
├── README.md / README.zh-CN.md
└── go.mod                     # Go 1.25.0
```

### 核心模块职责

| 模块 | 职责 |
|------|------|
| `httpapi` | HTTP 服务、请求解析、响应格式化（OpenAI/Anthropic 双协议） |
| `lingmaipc` | 底层 IPC 通信（Named Pipe/WebSocket）、JSON-RPC 协议 |
| `service` | 业务逻辑编排、会话生命周期管理、模型列表获取 |
| `toolemulation` | 工具调用支持（定义注入、解析、重编码、多轮历史） |

---

## 技术实现亮点

### 1. 配置优先级设计

支持四层配置覆盖（优先级从低到高）：
1. 内置默认值
2. JSON 配置文件
3. 环境变量
4. 命令行参数

### 2. 会话管理模式

三种会话模式满足不同场景：
- `reuse`: 持续复用 sticky 会话（适合单轮对话）
- `fresh`: 每次请求新建临时会话（适合多轮对话）
- `auto`: 智能判断（单轮复用，带 history/system 走 fresh）

### 3. 传输层自动发现

支持自动检测 Lingma 连接方式：
- 读取 `%APPDATA%/Lingma/SharedClientCache/.info.json`
- 自动扫描 `\\.\pipe\lingma-*` 命名管道
- 支持显式配置覆盖

### 4. 流式响应实现

通过 Go Channel 实现真正的异步流式：
- `GenerateStream` 返回 `(eventsCh, doneCh)`
- HTTP 层使用 `http.Flusher` 实时推送 SSE
- 支持 Anthropic 和 OpenAI 两种流式格式

### 5. 工具调用支持

完整实现 OpenAI / Anthropic 标准工具协议：
- 注入工具定义到对话上下文
- 解析模型动作输出，重编码为 `tool_calls` / `tool_use`
- 维护多轮工具调用历史并重新投影
- 包装工具结果为续写提示词
- 拒答检测与自动重试纠偏
- 支持 `parallel_tool_calls: false` 约束

---

## API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/` / `/health` | GET | 健康检查 |
| `/v1/models` | GET | 获取可用模型列表 |
| `/v1/messages` | POST | Anthropic 格式对话 |
| `/v1/chat/completions` | POST | OpenAI 格式对话 |

---

## 部署方式

### 1. 直接运行

```powershell
go run .\cmd\lingma-ipc-proxy --port 8095 --session-mode auto
```

### 2. 构建可执行文件

```powershell
.\scripts\build.ps1
# 输出: dist\lingma-ipc-proxy.exe
```

### 3. Windows 服务部署

支持 NSSM 和 WinSW 两种方式：

```powershell
# NSSM
.\scripts\install-nssm-service.ps1 -NssmPath C:\Tools\nssm\nssm.exe

# WinSW
.\scripts\install-winsw-service.ps1
.\LingmaIpcProxy.exe install
.\LingmaIpcProxy.exe start
```

---

## 代码质量

### 测试覆盖

- `transport_test.go`: 共享客户端信息解析、WebSocket URL 规范化
- `server_test.go`: 工具模拟历史消息处理

### 设计模式

- **分层架构**: 清晰的 API/Service/IPC 分层
- **信号量限流**: 使用 buffered channel 实现单请求限流
- **连接池管理**: sticky session + 连接复用
- **优雅关闭**: 信号监听 + context 超时控制

### 依赖项

```go
require (
    github.com/Microsoft/go-winio v0.6.2    // Windows Named Pipe
    github.com/gorilla/websocket v1.5.3     // WebSocket 支持
)
```

---

## 项目状态评估

### 已完成 ✅

- [x] 基础 HTTP API 服务（OpenAI/Anthropic 双协议）
- [x] Named Pipe 传输层
- [x] WebSocket 传输层
- [x] 流式响应支持（SSE）
- [x] 会话管理（reuse/fresh/auto 三种模式）
- [x] 模型列表获取
- [x] 配置文件支持（JSON）
- [x] 环境变量支持
- [x] Windows 服务部署脚本
- [x] 工具调用支持（完整 OpenAI / Anthropic 协议）
- [x] 多轮 Agent 循环（tool history 投影 + 结果回灌）
- [x] 图片输入支持（base64 / HTTP URL）
- [x] API 参数兼容（temperature、top_p、stop 等）
- [x] 跨平台支持（Windows / macOS / Linux）
- [x] 基础测试覆盖

### 项目状态

**完整可用**。代理层已实现 OpenAI 和 Anthropic 双协议的完整适配，支持文本对话、工具调用、图片输入、流式响应等全部核心功能，可直接对接 Claude Code、Continue、Cline 等客户端使用。

---

## 使用场景

1. **本地开发**: 将 Lingma 能力以标准 OpenAI API 暴露给其他工具
2. **IDE 集成**: 在 VS Code/Cursor 等工具中使用 Lingma 模型
3. **自动化脚本**: 通过标准 HTTP API 调用 Lingma 能力
4. **工具链集成**: 支持 function calling 的 agent 工作流

---

## 总结

这是一个**功能完整、架构清晰**的 IPC 代理项目。代码质量良好，分层合理，配置灵活，部署方便。核心能力是将 Lingma 的私有 IPC 协议转换为业界标准的 OpenAI/Anthropic API，使得 Lingma 可以无缝集成到更广泛的 AI 工具生态中。

项目已达到**可用状态**，可以作为生产环境的本地代理服务部署。
