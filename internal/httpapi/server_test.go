package httpapi

import "testing"

func TestNormalizeOpenAIRequestCollectsSystemMessages(t *testing.T) {
	req := openAIChatRequest{
		Model: "test-model",
		Messages: []rawMessage{
			{Role: "system", Content: "You are concise."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi"},
			{Role: "system", Content: "Answer in Chinese."},
			{Role: "tool", Content: "ignored"},
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "Follow up"},
			}},
		},
	}

	normalized, err := normalizeOpenAIRequest(req)
	if err != nil {
		t.Fatalf("normalizeOpenAIRequest() error = %v", err)
	}
	if normalized.Model != "test-model" {
		t.Fatalf("model = %q", normalized.Model)
	}
	if normalized.System != "You are concise.\n\nAnswer in Chinese." {
		t.Fatalf("system = %q", normalized.System)
	}
	if len(normalized.Messages) != 3 {
		t.Fatalf("message count = %d", len(normalized.Messages))
	}
	if normalized.Messages[0].Role != "user" || normalized.Messages[0].Text != "Hello" {
		t.Fatalf("first message = %+v", normalized.Messages[0])
	}
	if normalized.Messages[1].Role != "assistant" || normalized.Messages[1].Text != "Hi" {
		t.Fatalf("second message = %+v", normalized.Messages[1])
	}
	if normalized.Messages[2].Role != "user" || normalized.Messages[2].Text != "Follow up" {
		t.Fatalf("third message = %+v", normalized.Messages[2])
	}
}

func TestNormalizeOpenAIRequestRejectsMissingUserAndAssistantMessages(t *testing.T) {
	req := openAIChatRequest{
		Model: "test-model",
		Messages: []rawMessage{
			{Role: "system", Content: "Only system"},
			{Role: "tool", Content: "ignored"},
		},
	}

	_, err := normalizeOpenAIRequest(req)
	if err == nil {
		t.Fatal("expected error for request without user or assistant messages")
	}
}

func TestNormalizeAnthropicRequestExtractsStructuredText(t *testing.T) {
	req := anthropicRequest{
		Model:  "test-model",
		System: []any{map[string]any{"type": "text", "text": "System prompt"}},
		Messages: []rawMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "Hello"},
				},
			},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{"type": "text", "text": "Hi"},
				},
			},
			{
				Role: "metadata",
				Content: []any{
					map[string]any{"type": "text", "text": "ignored"},
				},
			},
		},
	}

	normalized, err := normalizeAnthropicRequest(req)
	if err != nil {
		t.Fatalf("normalizeAnthropicRequest() error = %v", err)
	}
	if normalized.Model != "test-model" {
		t.Fatalf("model = %q", normalized.Model)
	}
	if normalized.System != "System prompt" {
		t.Fatalf("system = %q", normalized.System)
	}
	if len(normalized.Messages) != 2 {
		t.Fatalf("message count = %d", len(normalized.Messages))
	}
	if normalized.Messages[0].Role != "user" || normalized.Messages[0].Text != "Hello" {
		t.Fatalf("first message = %+v", normalized.Messages[0])
	}
	if normalized.Messages[1].Role != "assistant" || normalized.Messages[1].Text != "Hi" {
		t.Fatalf("second message = %+v", normalized.Messages[1])
	}
}

func TestNormalizeAnthropicRequestRejectsEmptyMessages(t *testing.T) {
	req := anthropicRequest{
		Model: "test-model",
		Messages: []rawMessage{
			{Role: "user", Content: ""},
			{Role: "assistant", Content: nil},
		},
	}

	_, err := normalizeAnthropicRequest(req)
	if err == nil {
		t.Fatal("expected error for request without usable messages")
	}
}
