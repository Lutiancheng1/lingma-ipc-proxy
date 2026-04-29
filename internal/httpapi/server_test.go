package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lingma-ipc-proxy/internal/service"
)

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

func TestCapabilitiesAdvertiseAgentCompatibility(t *testing.T) {
	server := NewServer("", service.New(service.Config{
		Model:   "Qwen3-Coder",
		Timeout: time.Second,
	}))

	req := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	rec := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	features, ok := body["features"].(map[string]any)
	if !ok {
		t.Fatalf("missing features: %#v", body)
	}
	for _, key := range []string{"tools", "tool_alias_mapping", "images", "local_image_paths", "image_auto_resize"} {
		if features[key] != true {
			t.Fatalf("feature %s = %#v", key, features[key])
		}
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

func TestDiscoveryCompatibilityEndpoints(t *testing.T) {
	server := NewServer("", service.New(service.Config{
		Model:   "Qwen3-Coder",
		Timeout: time.Second,
	}))

	cases := []string{
		"/version",
		"/props",
		"/v1/props",
	}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body = %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestParseImageURLReadsLocalFileURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jpg")
	data := []byte{0xff, 0xd8, 0xff, 0xd9}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	img := parseImageURL("file://" + path)
	if img == nil {
		t.Fatal("expected image")
	}
	if img.MediaType != "image/jpeg" {
		t.Fatalf("media type = %q", img.MediaType)
	}
	if img.Data != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("data = %q", img.Data)
	}
}

func TestParseImageURLReadsAbsoluteLocalPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.png")
	data := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	img := parseImageURL(path)
	if img == nil {
		t.Fatal("expected image")
	}
	if img.MediaType != "image/png" {
		t.Fatalf("media type = %q", img.MediaType)
	}
	if img.Data != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("data = %q", img.Data)
	}
}

func TestSanitizeRecordedBodyRedactsImagePayloads(t *testing.T) {
	raw := []byte(`{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("a", 8192) + `"}}]}]}`)
	got := sanitizeRecordedBody(raw)
	if strings.Contains(got, "data:image/png;base64") {
		t.Fatalf("image payload was not redacted: %s", got)
	}
	if !strings.Contains(got, "[image payload redacted") {
		t.Fatalf("missing redaction marker: %s", got)
	}
}
