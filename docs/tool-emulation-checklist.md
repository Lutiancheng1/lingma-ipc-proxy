# Tool Calling Implementation Checklist

This checklist covers the complete implementation of OpenAI / Anthropic compatible tool calling over a plain chat API.

It breaks the work into concrete surfaces that can be implemented and validated incrementally.

## 1. Prompt Contract

- tell the model that tools are available
- list tool names, short descriptions, and schema summaries
- define a fixed action format
- define multi-turn rules
- encode `tool_choice` constraints
- include at least one valid action example
- ideally include one example where a tool result arrives and the model decides what to do next

Acceptance:

- the first turn reliably emits a valid action block
- later turns do not collapse into plain explanation after a tool result

## 2. Request Normalization

- OpenAI:
  - parse `tools`
  - parse `tool_choice`
  - parse `assistant.tool_calls`
  - parse `tool`
- Anthropic:
  - parse `tools`
  - parse `tool_choice`
  - parse `tool_use`
  - parse `tool_result`
- normalize everything into one internal structure
- detect tool history even when the current turn does not repeat `tools`

Acceptance:

- tool calling stays active on later turns without repeated tool definitions

## 3. Tool History Projection

- project historical assistant tool calls back into action text
- do not pass downstream protocol-specific history directly to Lingma
- preserve tool name, arguments, and call id where useful

Acceptance:

- the model can “see” its own previous actions in later turns

## 4. Tool Result Continuation

- do not feed raw tool output back without framing
- wrap tool results into an explicit continuation message
- handle empty, partial, and error outputs consistently

Acceptance:

- after a tool result, the model can either call another tool or finish naturally

## 5. Parser Contract

- recognize both ` ```json action ` and plain ` ```json `
- tolerate smart quotes, trailing commas, and stringified argument JSON
- extract `tool`, `name`, `parameters`, `arguments`, or `input`
- support multiple blocks in one reply
- strip action blocks from normal assistant text

Acceptance:

- multiple action blocks can be parsed reliably

## 6. Retry Policy

- trigger when:
  - a tool call was expected but no action block was produced
  - refusal language is detected
  - `tool_choice=any`
  - `tool_choice=tool`
- retry with a stricter message
- bound retry count
- log retry reason

Acceptance:

- refusal-style replies can be corrected without infinite loops

## 7. Refusal Detection

- maintain a refusal phrase set
- detect both hard refusals and soft “environment limitation” answers
- distinguish between:
  - a legitimate no-tool answer
  - a failed tool-use turn

Acceptance:

- common “tools are unavailable” replies trigger retry when appropriate

## 8. Response Re-encoding

- OpenAI:
  - emit `message.tool_calls`
  - set `finish_reason = tool_calls`
- Anthropic:
  - emit `content[].tool_use`
  - set `stop_reason = tool_use`
- preserve normal text when no tool call is present

Acceptance:

- downstream clients remain unaware that Lingma does not expose native tools

## 9. Streaming Strategy

- OpenAI:
  - role chunk
  - text deltas
  - tool call deltas
- Anthropic:
  - `message_start`
  - `content_block_start`
  - `content_block_delta`
  - `content_block_stop`
  - `message_delta`
  - `message_stop`
- document clearly when streaming is synthesized from a completed non-stream result

Acceptance:

- downstream stream consumers receive protocol-valid event sequences

## 10. Multi-turn State Machine

- distinguish at least:
  - first decision
  - tool call emitted
  - waiting for tool result
  - tool result received, next decision pending
  - final answer
- derive state from message history, not only the current payload
- do not confuse “tool history exists” with “another tool call is mandatory”

Acceptance:

- agent loops remain stable across more than one turn

## 11. Observability

- log:
  - whether tool calling is active
  - how many tool calls were parsed
  - whether retry fired
  - which refusal signal matched
- ideally log whether:
  - the prompt contract was injected
  - tool history was detected

Acceptance:

- failures can be localized to prompt, parser, retry, or state management

## 12. Test Matrix

- OpenAI:
  - single-turn tool call
  - multi-turn tool result continuation
  - later turn without repeated `tools`
  - forced tool
  - `tool_choice=any`
  - `tool_choice=none`
  - `parallel_tool_calls=false`
- Anthropic:
  - single-turn `tool_use`
  - multi-turn `tool_result` continuation
  - later turn without repeated `tools`
  - streaming `tool_use`
  - `tool_choice=any` / `tool_choice=none`
- error cases:
  - refusal
  - invalid JSON
  - multiple action blocks
  - plain-text final answer

Acceptance:

- both “first tool turn” and “second-turn continuation” are covered

## 13. Recommended Next Priorities

If the system already works, the highest-value next improvements are:

1. stronger few-shot for “tool result arrives, then call another tool”
2. better history-aware retry policy
3. finer refusal categories
4. stronger parser tolerance
5. richer streaming behavior
