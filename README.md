# lingma-ipc-proxy

[English](./README.md) | [简体中文](./README.zh-CN.md)

A standalone Go backend that talks to Lingma over Lingma's local pipe or websocket transport and exposes:

- `GET /v1/models`
- `POST /v1/messages`
- `POST /v1/chat/completions`

Current scope:

- supports both non-streaming and streaming responses
- one request at a time
- supports Windows named-pipe transport and local websocket transport
- directly uses Lingma IPC, not DOM/CDP

## Run

```powershell
cd C:\Workspace\Personal\lingma-ipc-proxy
go run .\cmd\lingma-ipc-proxy
```

## Config File

The proxy can load a JSON config file so you do not need to carry a long command line every time.

Default lookup:

```text
./lingma-ipc-proxy.json
```

You can also point to an explicit file:

```powershell
.\dist\lingma-ipc-proxy.exe --config .\config.example.json
```

Resolution order:

- built-in defaults
- JSON config file
- environment variables
- command-line flags

An example config is included at:

- `config.example.json`

A practical setup is to copy it to `lingma-ipc-proxy.json`, adjust the values once, and then start the proxy without a long flag list.

Recommended layout:

```json
{
  "host": "127.0.0.1",
  "port": 8095,
  "transport": "auto",
  "mode": "chat",
  "session_mode": "reuse",
  "timeout": 120,
  "cwd": "C:/Workspace/Personal/lingma-ipc-proxy",
  "shell_type": "powershell",
  "current_file_path": "",
  "pipe": "",
  "websocket_url": ""
}
```

## Build

Build a Windows executable:

```powershell
cd C:\Workspace\Personal\lingma-ipc-proxy
.\scripts\build.ps1
```

Default output:

```text
dist\lingma-ipc-proxy.exe
```

Direct Go build command:

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o .\dist\lingma-ipc-proxy.exe .\cmd\lingma-ipc-proxy
```

Run the built binary:

```powershell
.\dist\lingma-ipc-proxy.exe --host 127.0.0.1 --port 8095 --session-mode auto
.\dist\lingma-ipc-proxy.exe --transport websocket --ws-url ws://127.0.0.1:36510 --port 8095
```

## Windows Service

For this project, the correct deployment shape is a native local process, not Docker. The proxy talks to Lingma over local pipe or websocket transport, so it should run on the same host as Lingma itself.

### NSSM

Build first:

```powershell
.\scripts\build.ps1
```

Install with NSSM:

```powershell
.\scripts\install-nssm-service.ps1 -NssmPath C:\Tools\nssm\nssm.exe
```

This wraps:

```powershell
nssm.exe install LingmaIpcProxy C:\Workspace\Personal\lingma-ipc-proxy\dist\lingma-ipc-proxy.exe --host 127.0.0.1 --port 8095 --session-mode auto
nssm.exe set LingmaIpcProxy AppDirectory C:\Workspace\Personal\lingma-ipc-proxy
nssm.exe start LingmaIpcProxy
```

### WinSW

Prepare the executable:

```powershell
.\scripts\build.ps1
```

Put a WinSW binary at:

```text
dist\WinSW-x64.exe
```

Then generate the wrapper files:

```powershell
.\scripts\install-winsw-service.ps1
```

That script creates:

- `LingmaIpcProxy.exe`
- `LingmaIpcProxy.xml`

Then install/start:

```powershell
.\LingmaIpcProxy.exe install
.\LingmaIpcProxy.exe start
```

The WinSW XML template lives at:

- `scripts\lingma-ipc-proxy.xml.template`

## Flags

```powershell
go run .\cmd\lingma-ipc-proxy --port 8095 --session-mode auto
```

- `--host`
- `--port`
- `--transport`
- `--pipe`
- `--ws-url`
- `--cwd`
- `--current-file-path`
- `--mode`
- `--shell-type`
- `--session-mode`
  - `reuse`: keep using the sticky Lingma session
  - `fresh`: create a temporary session for the request and delete it after completion
  - `auto`: single-turn requests reuse; requests with system/history use a temporary fresh session and delete it after completion
- `--timeout`

## Environment

- `LINGMA_PROXY_TRANSPORT`
- `LINGMA_IPC_PIPE`
- `LINGMA_PROXY_WS_URL`
- `LINGMA_PROXY_HOST`
- `LINGMA_PROXY_PORT`
- `LINGMA_PROXY_CWD`
- `LINGMA_PROXY_CURRENT_FILE_PATH`
- `LINGMA_PROXY_MODE`
- `LINGMA_PROXY_SHELL_TYPE`
- `LINGMA_PROXY_SESSION_MODE`
- `LINGMA_PROXY_TIMEOUT_SECONDS`

## Examples

Anthropic non-streaming:

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

Anthropic streaming:

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

OpenAI non-streaming:

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

OpenAI streaming:

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

## Streaming shape

Anthropic streaming emits SSE events compatible with the `messages` API shape:

- `message_start`
- `content_block_start`
- `content_block_delta`
- `content_block_stop`
- `message_delta`
- `message_stop`

OpenAI streaming emits `chat.completion.chunk` payloads as `data:` lines and ends with:

- `data: [DONE]`
