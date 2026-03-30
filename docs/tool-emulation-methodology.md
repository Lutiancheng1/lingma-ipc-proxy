# Methodology: Simulating Tool Calls over a Plain Chat API

This document describes a practical pattern for supporting tool calling when the upstream model only exposes a plain chat API.

The core idea is:

1. Convert downstream tool definitions into a prompt-level contract.
2. Ask the model to emit structured action text.
3. Parse that action text in the proxy.
4. Re-encode it back into standard protocol fields such as OpenAI `tool_calls` or Anthropic `tool_use`.

## Core Pattern

When the upstream model does not support native tool calls, do not rely on blindly forwarding `tools`.

Instead:

- treat the model as a text generator
- define a stable action DSL
- keep the proxy responsible for state, retries, parsing, and protocol mapping

In this project the action DSL is a fenced block:

```text
```json action
{"tool":"NAME","parameters":{"key":"value"}}
```
```

## What the Proxy Must Do

The proxy is not a passive transport anymore. Once tool emulation is enabled, it should:

- inject tool definitions into the prompt
- preserve tool history across turns
- project historical tool calls back into action text
- wrap tool results into a continuation prompt
- detect refusal patterns such as “I don't have tools”
- retry with a stronger instruction when a tool call was expected but missing
- map parsed actions back into downstream protocol fields

## Multi-turn Tool Calling

Single-turn emulation is not enough. A useful agent loop looks like this:

1. model emits a tool call
2. external executor runs the tool
3. tool result is fed back into the conversation
4. model decides whether to call another tool or finish

To make this stable:

- do not feed tool results back as raw text only
- wrap them in a continuation message that clearly asks for the next action
- keep emulation active even when later turns do not repeat the original `tools` field

That last point matters. Many clients send `tools` only on the first turn. The proxy should still keep the conversation in emulation mode when it sees tool history.

## Few-shot Guidance

The minimum few-shot should teach the model the output shape.

A better few-shot also teaches state transitions:

- when to call a tool
- when to wait for the tool result
- when to call another tool
- when to answer normally

For complex agent loops, a multi-step example with:

- user request
- assistant tool call
- tool result
- assistant next action

is usually more effective than a single static action example.

## Retry Guidance

Retry is useful when:

- a tool call was expected but no action block was produced
- the model says tools are unavailable
- the request forces tool usage

A retry prompt should be explicit and procedural, for example:

```text
Your last response did not include any ```json action``` block.
You must respond with at least one valid action block now.
Do not explain. Output the action block directly.
```

Retries should be bounded. A small retry budget plus stronger instructions per retry is usually enough.

## Protocol Mapping

OpenAI side:

- input may contain `tools`, `tool_choice`, `assistant.tool_calls`, and `tool`
- output should map back into `message.tool_calls` and `finish_reason = "tool_calls"`

Anthropic side:

- input may contain `tools`, `tool_choice`, `tool_use`, and `tool_result`
- output should map back into `content[].tool_use` and `stop_reason = "tool_use"`

## Common Failure Modes

- only supporting the first tool turn
- losing emulation state on later turns
- not projecting historical tool calls back into text
- feeding back raw tool results without continuation instructions
- missing refusal detection
- using a parser that is too brittle for real model output

## In This Repository

The implementation here follows exactly this pattern:

- downstream tool schemas are rewritten into prompt instructions
- the model emits `json action` blocks
- the proxy parses them
- the proxy re-encodes them as OpenAI or Anthropic tool protocol fields
- later turns can continue from tool history even when `tools` are not repeated

Implementation checklist:

- [tool-emulation-checklist.md](./tool-emulation-checklist.md)

