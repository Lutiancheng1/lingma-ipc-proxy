# Changelog

## Unreleased

- Nothing yet.

## v1.4.3 - 2026-04-30

- Added remote API timeout fallback with a configurable model order. The default order is Kimi-K2.6, MiniMax-M2.7, Qwen3-Coder, Qwen3.6-Plus, Qwen3-Max, and Qwen3-Thinking.
- Fallback only runs before any streaming bytes are sent and only uses models returned by the active `/v1/models` response.
- Changed the default request timeout from 120 seconds to 300 seconds.
- Added a desktop Settings switch and fallback model list editor.
- Added persistent desktop app state for request history, app logs, and cumulative token usage.
- Added a Dashboard token usage card and model-list specification chips for context window and capability summaries.
- Added model display to the desktop request stream table and model-aware request search.
- Fixed Dashboard "recent model" tracking so health/model-list requests no longer override the last real chat model.
- Updated architecture documentation to cover the IPC and Remote API dual-backend design.
- Disabled desktop Inspector and default context menu in production builds; local development can opt in with `LINGMA_DESKTOP_DEBUG=1`.

## v1.4.2 - 2026-04-30

- Default backend changed to remote API mode for new CLI and desktop configurations.
- Default model changed to `kmodel` (`Kimi-K2.6` in Lingma remote model list).
- Removed the proxy-injected fake `Auto` model in remote mode so the model list only shows models returned by Lingma.
- Fixed Dashboard recent requests showing `MiniMax-M2.7` for model discovery and health/debug requests that do not contain a model field.
- Added request record model and payload size fields for the desktop app request table.
- Updated Dashboard transport display to show `Remote API` when remote backend is active.
- Updated Hermes local config to use Lingma Proxy with `kmodel` and remote model IDs.
- Updated README / README.zh-CN for remote-first mode, Kimi recommendation, package selection, protocol support, and debug/log endpoints.

## v1.4.1 - 2026-04-30

- Improved remote enterprise endpoint detection from Lingma logs.
- Added support for showing detected remote base URL and credential source in desktop Settings.
- Added macOS DMG packaging in GitHub Actions.

## v1.4.0 - 2026-04-30

- Added experimental remote API backend alongside the original IPC plugin backend.
- Added remote credential import from local Lingma login cache or explicit credential files.
- Added OpenAI / Anthropic compatible routing over the remote backend.
- Added request and log debug endpoints for troubleshooting.
