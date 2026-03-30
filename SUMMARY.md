# lingma-ipc-proxy 项目总结

## 项目概述

**lingma-ipc-proxy** 是一个独立的 Go 后端服务，通过 Lingma 本地 pipe 或 WebSocket 传输与其通信，对外暴露兼容 OpenAI 和 Anthropic 的 API 接口。

### 核心功能

- **API 兼容性**: 支持 OpenAI (`/v1/chat/completions`) 和 Anthropic (`/v1/messages`) 格式的 API
- **流式与非流式响应**: 完整支持 SSE 流式输出和普通 JSON 响应
- **双传输层**: 支持 Windows Named Pipe 和 WebSocket 两种传输方式
- **直接 IPC 通信**: 直接与 Lingma 进程通信，不依赖 DOM/CDP
- **工具调用模拟**: 通过 prompt 注入方式模拟工具调用能力

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
| `toolemulation` | 工具调用模拟（通过 prompt 注入实现） |

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

### 5. 工具调用模拟

不依赖原生工具支持，通过 prompt 工程实现：
- 注入工具定义到 system prompt
- 要求模型输出 `\`\`\`json action` 代码块
- 解析 Action Block 转换为 Tool Call
- 支持工具结果回传继续对话

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
- [x] 工具调用模拟（prompt 注入方式）
- [x] 基础测试覆盖

### 技术债务/待优化 ⚠️

- [ ] 工具模拟目前通过 prompt 注入，非原生支持
- [ ] 单请求限流（channel buffer=1）可能成为瓶颈
- [ ] 仅支持 Windows（Named Pipe 依赖）
- [ ] 测试覆盖率可进一步提升

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
