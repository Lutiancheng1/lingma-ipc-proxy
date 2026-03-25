# lingma-ipc-proxy

[English](./README.md) | [简体中文](./README.zh-CN.md)

`lingma-ipc-proxy` 是一个独立的 Go 后端，通过 Windows Named Pipe IPC 与 Lingma 通信，并对外暴露：

- `GET /v1/models`
- `POST /v1/messages`
- `POST /v1/chat/completions`

当前范围：

- 支持非流式与流式响应
- 单次只处理一个请求
- 仅支持 Windows
- 直接走 Lingma IPC，不依赖 DOM/CDP

## 运行

```powershell
cd C:\Workspace\Personal\lingma-ipc-proxy
go run .\cmd\lingma-ipc-proxy
```

## 构建

构建 Windows 可执行文件：

```powershell
cd C:\Workspace\Personal\lingma-ipc-proxy
.\scripts\build.ps1
```

默认输出：

```text
dist\lingma-ipc-proxy.exe
```

等价的 Go 构建命令：

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o .\dist\lingma-ipc-proxy.exe .\cmd\lingma-ipc-proxy
```

运行构建后的二进制：

```powershell
.\dist\lingma-ipc-proxy.exe --host 127.0.0.1 --port 8095 --session-mode auto
```

## Windows 服务

这个项目正确的部署形态是 Windows 本机进程，不是 Docker。原因很直接：代理需要通过 Windows named pipe 与本机 Lingma 通信，所以必须和 Lingma 跑在同一台 Windows 主机上。

### NSSM

先构建：

```powershell
.\scripts\build.ps1
```

再用 NSSM 安装：

```powershell
.\scripts\install-nssm-service.ps1 -NssmPath C:\Tools\nssm\nssm.exe
```

它等价于执行：

```powershell
nssm.exe install LingmaIpcProxy C:\Workspace\Personal\lingma-ipc-proxy\dist\lingma-ipc-proxy.exe --host 127.0.0.1 --port 8095 --session-mode auto
nssm.exe set LingmaIpcProxy AppDirectory C:\Workspace\Personal\lingma-ipc-proxy
nssm.exe start LingmaIpcProxy
```

### WinSW

先准备可执行文件：

```powershell
.\scripts\build.ps1
```

把 WinSW 二进制放到：

```text
dist\WinSW-x64.exe
```

然后生成服务包装文件：

```powershell
.\scripts\install-winsw-service.ps1
```

脚本会生成：

- `LingmaIpcProxy.exe`
- `LingmaIpcProxy.xml`

然后安装并启动：

```powershell
.\LingmaIpcProxy.exe install
.\LingmaIpcProxy.exe start
```

WinSW 模板文件位置：

- `scripts\lingma-ipc-proxy.xml.template`

## 启动参数

```powershell
go run .\cmd\lingma-ipc-proxy --port 8095 --session-mode auto
```

- `--host`
- `--port`
- `--pipe`
- `--cwd`
- `--current-file-path`
- `--mode`
- `--shell-type`
- `--session-mode`
  - `reuse`：持续复用 sticky Lingma 会话
  - `fresh`：为本次请求创建临时会话，结束后自动删除
  - `auto`：单轮请求复用会话；带 system/history 的请求走临时 fresh 会话并在结束后自动删除
- `--timeout`

## 环境变量

- `LINGMA_IPC_PIPE`
- `LINGMA_PROXY_HOST`
- `LINGMA_PROXY_PORT`
- `LINGMA_PROXY_CWD`
- `LINGMA_PROXY_CURRENT_FILE_PATH`
- `LINGMA_PROXY_MODE`
- `LINGMA_PROXY_SHELL_TYPE`
- `LINGMA_PROXY_SESSION_MODE`
- `LINGMA_PROXY_TIMEOUT_SECONDS`

## 示例

Anthropic 非流式：

```powershell
$body = @{
  model = "dashscope_qwen3_coder"
  messages = @(
    @{ role = "user"; content = "请只回复：ANTHROPIC_OK" }
  )
  stream = $false
} | ConvertTo-Json -Depth 8

Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8095/v1/messages `
  -ContentType "application/json" `
  -Body $body
```

Anthropic 流式：

```powershell
$body = @{
  model = "dashscope_qwen3_coder"
  messages = @(
    @{ role = "user"; content = "请只回复：ANTHROPIC_STREAM_OK" }
  )
  stream = $true
} | ConvertTo-Json -Depth 8

curl.exe -N `
  -H "Content-Type: application/json" `
  -d $body `
  http://127.0.0.1:8095/v1/messages
```

OpenAI 非流式：

```powershell
$body = @{
  model = "dashscope_qwen3_coder"
  messages = @(
    @{ role = "user"; content = "请只回复：OPENAI_OK" }
  )
  stream = $false
} | ConvertTo-Json -Depth 8

Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8095/v1/chat/completions `
  -ContentType "application/json" `
  -Body $body
```

OpenAI 流式：

```powershell
$body = @{
  model = "dashscope_qwen3_coder"
  messages = @(
    @{ role = "user"; content = "请只回复：OPENAI_STREAM_OK" }
  )
  stream = $true
} | ConvertTo-Json -Depth 8

curl.exe -N `
  -H "Content-Type: application/json" `
  -d $body `
  http://127.0.0.1:8095/v1/chat/completions
```

## 流式返回形状

Anthropic 流式响应会输出与 `messages` API 兼容的 SSE 事件：

- `message_start`
- `content_block_start`
- `content_block_delta`
- `content_block_stop`
- `message_delta`
- `message_stop`

OpenAI 流式响应会输出 `chat.completion.chunk` 形状的 `data:` 行，并以：

- `data: [DONE]`

结束。
