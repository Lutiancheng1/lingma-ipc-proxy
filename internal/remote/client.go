package remote

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://lingma.alibabacloud.com"
	chatPath       = "/algo/api/v2/service/pro/sse/agent_chat_generation"
	chatQuery      = "?FetchKeys=llm_model_result&AgentId=agent_common"
	modelListPath  = "/algo/api/v2/model/list"
)

type Config struct {
	BaseURL     string
	AuthFile    string
	CosyVersion string
	Timeout     time.Duration
}

type Client struct {
	cfg    Config
	client *http.Client
}

type Model struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Model       string `json:"model"`
	Enable      bool   `json:"enable"`
}

type ChatRequest struct {
	Model       string
	Prompt      string
	Stream      bool
	Temperature *float64
}

type ChatResult struct {
	Text          string
	InputTokens   int
	OutputTokens  int
	RequestID     string
	CredentialSrc string
}

type StreamEvent struct {
	Delta string
}

func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = ResolveBaseURL("")
	}
	if cfg.CosyVersion == "" {
		cfg.CosyVersion = "2.11.2"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

func ResolveBaseURL(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimRight(strings.TrimSpace(explicit), "/")
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_REMOTE_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	for _, path := range candidateConfigFiles() {
		if value := readBaseURLHint(path); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	return DefaultBaseURL
}

func (c *Client) Warmup(ctx context.Context) error {
	_, err := LoadCredential(c.cfg.AuthFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err = c.ListModels(ctx)
	return err
}

func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	cred, err := LoadCredential(c.cfg.AuthFile)
	if err != nil {
		return nil, err
	}
	headers, err := c.headers(cred, modelListPath, "")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+modelListPath, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("remote model list status %d: %s", resp.StatusCode, truncate(string(body), 500))
	}
	var payload struct {
		Chat   []Model `json:"chat"`
		Inline []Model `json:"inline"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return append(payload.Chat, payload.Inline...), nil
}

func (c *Client) Chat(ctx context.Context, request ChatRequest, onDelta func(string)) (*ChatResult, error) {
	cred, err := LoadCredential(c.cfg.AuthFile)
	if err != nil {
		return nil, err
	}
	requestID := newHexID()
	body, err := c.buildBody(requestID, request)
	if err != nil {
		return nil, err
	}
	headers, err := c.headers(cred, chatPath, body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+chatPath+chatQuery, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote chat status %d: %s", resp.StatusCode, truncate(string(respBody), 1000))
	}
	var builder strings.Builder
	if err := scanSSE(resp.Body, func(event sseEvent) error {
		if event.Done {
			return nil
		}
		if event.Content == "" {
			return nil
		}
		builder.WriteString(event.Content)
		if onDelta != nil {
			onDelta(event.Content)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	text := builder.String()
	return &ChatResult{
		Text:          text,
		InputTokens:   estimateTokens(request.Prompt),
		OutputTokens:  estimateTokens(text),
		RequestID:     requestID,
		CredentialSrc: cred.Source,
	}, nil
}

func (c *Client) buildBody(requestID string, request ChatRequest) (string, error) {
	temperature := 0.1
	if request.Temperature != nil {
		temperature = *request.Temperature
	}
	model := strings.TrimSpace(request.Model)
	if strings.EqualFold(model, "auto") {
		model = ""
	}
	payload := map[string]any{
		"request_id":       requestID,
		"request_set_id":   "",
		"chat_record_id":   requestID,
		"stream":           true,
		"image_urls":       nil,
		"is_reply":         false,
		"is_retry":         false,
		"session_id":       "",
		"code_language":    "",
		"source":           0,
		"version":          "3",
		"chat_prompt":      "",
		"parameters":       map[string]float64{"temperature": temperature},
		"aliyun_user_type": "personal_standard",
		"agent_id":         "agent_common",
		"task_id":          "question_refine",
		"model_config": map[string]any{
			"key":          model,
			"display_name": "",
			"model":        model,
			"format":       "",
			"is_vl":        false,
			"is_reasoning": false,
			"api_key":      "",
			"url":          "",
			"source":       "",
			"enable":       false,
		},
		"messages": []map[string]any{{
			"role":    "user",
			"content": request.Prompt,
			"response_meta": map[string]any{
				"id": "",
				"usage": map[string]int{
					"prompt_tokens":     0,
					"completion_tokens": 0,
					"total_tokens":      0,
				},
			},
			"reasoning_content_signature": "",
		}},
		"business": map[string]any{
			"product":  "jb_plugin",
			"version":  c.cfg.CosyVersion,
			"type":     "memory",
			"id":       newUUID(),
			"begin_at": time.Now().UnixMilli(),
			"stage":    "start",
			"name":     "memory_intent_recognition_" + requestID,
		},
	}
	body, err := json.Marshal(payload)
	return string(body), err
}

func (c *Client) headers(cred Credential, path string, body string) (map[string]string, error) {
	if err := validateCredential(cred); err != nil {
		return nil, err
	}
	date := strconv.FormatInt(time.Now().Unix(), 10)
	authPayload := map[string]string{
		"cosyVersion": c.cfg.CosyVersion,
		"ideVersion":  "",
		"info":        cred.EncryptUserInfo,
		"requestId":   newUUID(),
		"version":     "v1",
	}
	authPayloadBytes, err := json.Marshal(authPayload)
	if err != nil {
		return nil, err
	}
	payloadBase64 := base64.StdEncoding.EncodeToString(authPayloadBytes)
	preimage := strings.Join([]string{
		payloadBase64,
		cred.CosyKey,
		date,
		body,
		normalizePath(path),
	}, "\n")
	signature := md5.Sum([]byte(preimage))
	return map[string]string{
		"Authorization":     fmt.Sprintf("Bearer COSY.%s.%x", payloadBase64, signature),
		"Content-Type":      "application/json",
		"Appcode":           "cosy",
		"Cosy-Date":         date,
		"Cosy-Key":          cred.CosyKey,
		"Cosy-Machineid":    cred.MachineID,
		"Cosy-User":         cred.UserID,
		"Cosy-Clientip":     "198.18.0.1",
		"Cosy-Clienttype":   "2",
		"Cosy-Machineos":    "x86_64_windows",
		"Cosy-Machinetoken": "",
		"Cosy-Machinetype":  "",
		"Cosy-Version":      c.cfg.CosyVersion,
		"Login-Version":     "v2",
		"User-Agent":        "lingma-ipc-proxy/remote",
		"Accept":            "text/event-stream",
		"Cache-Control":     "no-cache",
	}, nil
}

func normalizePath(path string) string {
	return strings.TrimPrefix(path, "/algo")
}

type outerSSE struct {
	Body       string `json:"body"`
	StatusCode int    `json:"statusCodeValue"`
}

type innerSSE struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

type sseEvent struct {
	Content string
	Done    bool
}

func scanSSE(reader io.Reader, onEvent func(sseEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			return onEvent(sseEvent{Done: true})
		}
		event, ok, err := parseSSEPayload(payload)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := onEvent(event); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func parseSSEPayload(payload string) (sseEvent, bool, error) {
	var outer outerSSE
	if err := json.Unmarshal([]byte(payload), &outer); err != nil {
		return sseEvent{}, false, err
	}
	if outer.StatusCode >= 400 {
		return sseEvent{}, false, fmt.Errorf("remote sse status %d", outer.StatusCode)
	}
	if outer.Body == "" {
		return sseEvent{}, false, nil
	}
	if outer.Body == "[DONE]" {
		return sseEvent{Done: true}, true, nil
	}
	var inner innerSSE
	if err := json.Unmarshal([]byte(outer.Body), &inner); err != nil {
		return sseEvent{}, false, err
	}
	var builder strings.Builder
	for _, choice := range inner.Choices {
		builder.WriteString(choice.Delta.Content)
	}
	return sseEvent{Content: builder.String()}, true, nil
}

func candidateConfigFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".lingma", "extension", "server", "config.json"),
		filepath.Join(home, ".lingma", "extension", "local", "config.json"),
		filepath.Join(home, ".lingma", "bin", "config.json"),
		filepath.Join(home, ".config", "lingma-ipc-proxy", "config.json"),
	}
}

func readBaseURLHint(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		text := string(body)
		if strings.Contains(text, "lingma.alibabacloud.com") {
			return DefaultBaseURL
		}
		return ""
	}
	return findBaseURL(value)
}

func findBaseURL(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "base") || strings.Contains(lower, "domain") || strings.Contains(lower, "url") {
				if text, ok := item.(string); ok && strings.HasPrefix(strings.TrimSpace(text), "http") && strings.Contains(text, "lingma") {
					return strings.TrimSpace(text)
				}
			}
			if nested := findBaseURL(item); nested != "" {
				return nested
			}
		}
	case []any:
		for _, item := range typed {
			if nested := findBaseURL(item); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return len([]rune(text)) / 4
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "... [truncated]"
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

var hexCounter uint64
