# lingma-ipc-proxy

A standalone Go backend that talks to Lingma over Windows named-pipe IPC and exposes:

- `GET /v1/models`
- `POST /v1/messages`
- `POST /v1/chat/completions`

Current scope:

- non-streaming only
- one request at a time
- Windows only
- directly uses Lingma IPC, not DOM/CDP

## Run

```powershell
cd C:\Workspace\Personal\lingma-ipc-proxy
go run .\cmd\lingma-ipc-proxy
```

## Flags

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
  - `reuse`: keep using the sticky Lingma session
  - `fresh`: create a temporary session for the request and delete it after completion
  - `auto`: single-turn requests reuse; requests with system/history use a temporary fresh session and delete it after completion
- `--timeout`

## Environment

- `LINGMA_IPC_PIPE`
- `LINGMA_PROXY_HOST`
- `LINGMA_PROXY_PORT`
- `LINGMA_PROXY_CWD`
- `LINGMA_PROXY_CURRENT_FILE_PATH`
- `LINGMA_PROXY_MODE`
- `LINGMA_PROXY_SHELL_TYPE`
- `LINGMA_PROXY_SESSION_MODE`
- `LINGMA_PROXY_TIMEOUT_SECONDS`

## Examples

Anthropic:

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

OpenAI:

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
