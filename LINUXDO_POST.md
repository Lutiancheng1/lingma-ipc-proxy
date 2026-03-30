# 把 Lingma 变成 OpenAI 兼容的 API 服务

> 写了一个 IPC 代理，让 Lingma 可以用标准 OpenAI API 调用

## 缘起

事情要从 [opencli](https://github.com/jackwener/opencli) 说起。

当时想给它写个插件扩展，本来打算用 Electron 做 UI，通过浏览器 DevTools 协议跟 Lingma 通信。但这样太绕了，性能也不好。

后来偶然看 Lingma 的日志，发现它在本地启了一个 Named Pipe：

```
\\\\.\\pipe\\lingma-xxxxxx
```

顺着这个线索翻进程文件，在 `%APPDATA%/Lingma/SharedClientCache/.info.json` 里发现了这个：

```json
{
  "websocketPort": 36510,
  "pid": 14060,
  "ipcServerPath": "\\\\.\\pipe\\lingma-bf0f32"
}
```

原来 Lingma 启动时会把自己的通信地址写在这个文件里。不需要走浏览器，直接连 pipe 就行。有了地址，就可以连上去了。

## 是什么

`lingma-ipc-proxy` 是一个本地代理服务，通过 Named Pipe 或 WebSocket 与 Lingma 通信，对外暴露兼容 OpenAI / Anthropic 的 HTTP API。

这样你就可以在 Cursor、Claude Desktop、或其他支持 OpenAI API 的工具里用 Lingma 的模型了。

## 能做什么

- ✅ 兼容 `/v1/chat/completions` 和 `/v1/messages`
- ✅ 支持流式输出 (SSE)
- ✅ 支持工具调用（通过 prompt 模拟）
- ✅ 自动发现 Lingma 连接（pipe 或 websocket）
- ✅ 可配置会话模式：复用 / 临时 / 自动

## 快速开始

```powershell
# 克隆
cd C:\Workspace\Personal\lingma-ipc-proxy

# 直接运行
go run .\cmd\lingma-ipc-proxy --port 8095

# 或者构建 exe
.\scripts\build.ps1
.\dist\lingma-ipc-proxy.exe
```

## 使用示例

```powershell
$body = @{
  model = "dashscope_qwen3_coder"
  messages = @(@{ role = "user"; content = "你好" })
  stream = $false
} | ConvertTo-Json

Invoke-RestMethod -Method Post `
  -Uri http://127.0.0.1:8095/v1/chat/completions `
  -ContentType "application/json" `
  -Body $body
```

## 一些技术细节

**传输层自动发现**
- 先读 `%APPDATA%/Lingma/SharedClientCache/.info.json`
- 找不到就扫 `\\.\pipe\lingma-*`
- 也支持手动指定 pipe 或 ws 地址

**会话管理三种模式**
- `reuse`: 一直用同一个会话（适合单轮）
- `fresh`: 每次新建临时会话（适合多轮长对话）
- `auto`: 单轮复用，带 history 走 fresh（默认）

**工具调用怎么做的**
Lingma 原生不支持工具调用，所以用 prompt 注入的方式：
1. 把工具定义塞进 system prompt
2. 让模型输出 ` ```json action` 代码块
3. 代理解析 action block 转成 tool_call 格式
4. 工具结果再塞回去继续对话

## 部署为 Windows 服务

```powershell
# 用 NSSM
.\scripts\install-nssm-service.ps1 -NssmPath C:\Tools\nssm\nssm.exe

# 或者用 WinSW
.\scripts\install-winsw-service.ps1
.\LingmaIpcProxy.exe install
.\LingmaIpcProxy.exe start
```

## 项目结构

```
cmd/lingma-ipc-proxy/    # 入口
internal/
  httpapi/               # HTTP 层，处理 OpenAI/Anthropic 格式
  lingmaipc/             # IPC 层，pipe/ws 传输
  service/               # 业务层，会话管理
  toolemulation/         # 工具模拟
```

纯 Go 实现，只依赖 `go-winio` 和 `gorilla/websocket`。

## 代码质量

- 分层清晰，没搞过度设计
- 配置优先级：默认值 → JSON → 环境变量 → 命令行
- 有基础测试覆盖
- 支持优雅关闭

## 局限

- 目前只支持 Windows（因为 Named Pipe）
- 单请求限流（channel buffer=1）
- 工具调用是 prompt 模拟的，不是原生支持

> 有大佬如果能扒出 Lingma 原生工具调用的协议，那最好不过了。目前我是通过 prompt 注入让模型输出 action block，再解析成 tool_call，能用但不够优雅。

## 仓库

代码在自托管仓库，有兴趣可以看看实现细节。

---

有类似需求的可以交流，也欢迎提建议。
