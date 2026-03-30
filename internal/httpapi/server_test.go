package httpapi

import (
	"strings"
	"testing"
)

func TestNormalizeOpenAIRequestKeepsEmulationForToolHistoryWithoutTools(t *testing.T) {
	s := &Server{cfg: Config{EmulateToolCalls: true}}
	req := openAIChatRequest{
		Model: "test-model",
		Messages: []rawMessage{
			{
				Role:    "user",
				Content: "Call ping once, then after the tool result reply FINAL_OK.",
			},
			{
				Role:    "assistant",
				Content: nil,
				ToolCalls: []rawToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{
							Name:      "ping",
							Arguments: "{\"value\":\"x\"}",
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call_1",
				Content:    "pong",
			},
		},
	}

	normalized, err := s.normalizeOpenAIRequest(req)
	if err != nil {
		t.Fatalf("normalizeOpenAIRequest() error = %v", err)
	}
	if !normalized.Emulated {
		t.Fatalf("expected emulation to stay enabled when tool history exists")
	}
	if len(normalized.ChatRequest.Messages) != 3 {
		t.Fatalf("message count = %d", len(normalized.ChatRequest.Messages))
	}
	if normalized.ChatRequest.Messages[1].Role != "assistant" {
		t.Fatalf("assistant message role = %q", normalized.ChatRequest.Messages[1].Role)
	}
	if !strings.Contains(normalized.ChatRequest.Messages[1].Text, "json action") || !strings.Contains(normalized.ChatRequest.Messages[1].Text, "\"tool\": \"ping\"") {
		t.Fatalf("assistant tool call was not rewritten into action format: %q", normalized.ChatRequest.Messages[1].Text)
	}
	if normalized.ChatRequest.Messages[2].Role != "user" {
		t.Fatalf("tool result role = %q", normalized.ChatRequest.Messages[2].Role)
	}
	if !strings.Contains(normalized.ChatRequest.Messages[2].Text, "pong") {
		t.Fatalf("tool result was not converted into a follow-up prompt: %q", normalized.ChatRequest.Messages[2].Text)
	}
}

func TestNormalizeAnthropicRequestKeepsEmulationForToolHistoryWithoutTools(t *testing.T) {
	s := &Server{cfg: Config{EmulateToolCalls: true}}
	req := anthropicRequest{
		Model: "test-model",
		Messages: []rawMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "Use ping, then after the tool result reply FINAL_OK."},
				},
			},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{"type": "tool_use", "id": "call_1", "name": "ping", "input": map[string]any{"value": "x"}},
				},
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": []any{map[string]any{"type": "text", "text": "pong"}}},
				},
			},
		},
	}

	normalized, err := s.normalizeAnthropicRequest(req)
	if err != nil {
		t.Fatalf("normalizeAnthropicRequest() error = %v", err)
	}
	if !normalized.Emulated {
		t.Fatalf("expected emulation to stay enabled when anthropic tool history exists")
	}
	if len(normalized.ChatRequest.Messages) != 3 {
		t.Fatalf("message count = %d", len(normalized.ChatRequest.Messages))
	}
	if normalized.ChatRequest.Messages[1].Role != "assistant" {
		t.Fatalf("assistant message role = %q", normalized.ChatRequest.Messages[1].Role)
	}
	if !strings.Contains(normalized.ChatRequest.Messages[1].Text, "json action") || !strings.Contains(normalized.ChatRequest.Messages[1].Text, "\"tool\": \"ping\"") {
		t.Fatalf("assistant tool_use was not rewritten into action format: %q", normalized.ChatRequest.Messages[1].Text)
	}
	if normalized.ChatRequest.Messages[2].Role != "user" {
		t.Fatalf("tool result role = %q", normalized.ChatRequest.Messages[2].Role)
	}
	if !strings.Contains(normalized.ChatRequest.Messages[2].Text, "pong") {
		t.Fatalf("tool result was not converted into a follow-up prompt: %q", normalized.ChatRequest.Messages[2].Text)
	}
}
