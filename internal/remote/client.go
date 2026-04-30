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
	"net/url"
	"os"
	"path/filepath"
	"sort"
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

type BaseURLHint struct {
	URL    string
	Source string
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
	return ResolveBaseURLWithSource(explicit).URL
}

func ResolveBaseURLWithSource(explicit string) BaseURLHint {
	if strings.TrimSpace(explicit) != "" {
		return BaseURLHint{URL: strings.TrimRight(strings.TrimSpace(explicit), "/"), Source: "explicit config"}
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_REMOTE_BASE_URL")); value != "" {
		return BaseURLHint{URL: strings.TrimRight(value, "/"), Source: "LINGMA_REMOTE_BASE_URL"}
	}
	for _, path := range candidateConfigFiles() {
		if value := readBaseURLHint(path); value != "" {
			return BaseURLHint{URL: strings.TrimRight(value, "/"), Source: path}
		}
	}
	return BaseURLHint{URL: DefaultBaseURL, Source: "default"}
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
		"Cosy-Machineos":    MachineOSHeader(),
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
	paths := []string{
		filepath.Join(home, ".lingma", "extension", "server", "config.json"),
		filepath.Join(home, ".lingma", "extension", "local", "config.json"),
		filepath.Join(home, ".lingma", "bin", "config.json"),
		filepath.Join(home, ".config", "lingma-ipc-proxy", "config.json"),
		filepath.Join(home, ".lingma", "logs", "lingma.log"),
		filepath.Join(home, ".lingma", "logs", "lingma-extension.log"),
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "logs", "lingma.log"),
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "logs", "lingma-extension.log"),
	}
	for _, root := range lingmaLogRoots(home) {
		paths = append(paths, recentLingmaAppLogs(root)...)
	}
	return paths
}

func readBaseURLHint(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return extractBaseURLFromText(string(body))
	}
	if value := findBaseURL(value); value != "" {
		return value
	}
	return extractBaseURLFromText(string(body))
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

func lingmaLogRoots(home string) []string {
	roots := []string{
		filepath.Join(home, ".lingma", "logs"),
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "logs"),
		filepath.Join(home, "Library", "Application Support", "Lingma", "logs"),
	}
	for _, envName := range []string{"APPDATA", "LOCALAPPDATA", "ProgramData"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			roots = append(roots,
				filepath.Join(value, "Lingma", "logs"),
				filepath.Join(value, "Code", "User", "globalStorage", "alibaba-cloud.tongyi-lingma", "logs"),
			)
		}
	}
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		roots = append(roots, filepath.Join(value, "Lingma", "logs"))
	}
	if value := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); value != "" {
		roots = append(roots, filepath.Join(value, "Lingma", "logs"))
	}
	roots = append(roots,
		filepath.Join(home, ".config", "Lingma", "logs"),
		filepath.Join(home, ".local", "state", "Lingma", "logs"),
	)
	return uniqueStrings(roots)
}

func recentLingmaAppLogs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type logDir struct {
		path    string
		modTime int64
	}
	dirs := make([]logDir, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, logDir{path: filepath.Join(root, entry.Name()), modTime: info.ModTime().UnixNano()})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].modTime > dirs[j].modTime })
	if len(dirs) > 5 {
		dirs = dirs[:5]
	}
	paths := make([]string, 0, len(dirs)*4)
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir.path, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			name := entry.Name()
			lowerName := strings.ToLower(name)
			if lowerName == "renderer.log" ||
				lowerName == "sharedprocess.log" ||
				lowerName == "main.log" ||
				strings.HasSuffix(name, "Lingma.log") ||
				strings.Contains(lowerName, "lingma") && strings.HasSuffix(lowerName, ".log") {
				paths = append(paths, path)
			}
			return nil
		})
	}
	return paths
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func extractBaseURLFromText(text string) string {
	for _, marker := range []string{
		"endpoint config:",
		"Using service url:",
		"Download asset from:",
		"https://ai-lingma",
		"https://lingma",
	} {
		if value := extractBaseURLAfterMarker(text, marker); value != "" {
			return value
		}
	}
	return ""
}

func extractBaseURLAfterMarker(text, marker string) string {
	lowerText := strings.ToLower(text)
	lowerMarker := strings.ToLower(marker)
	index := strings.LastIndex(lowerText, lowerMarker)
	if index < 0 {
		return ""
	}
	tail := text[index+len(marker):]
	if strings.HasPrefix(lowerMarker, "https://") {
		tail = marker + tail
	}
	for _, field := range strings.Fields(tail) {
		field = strings.Trim(field, `"'<>),]}`)
		if value := normalizeRemoteBaseURLHint(field); value != "" {
			return value
		}
	}
	return ""
}

func normalizeRemoteBaseURLHint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	host := strings.ToLower(parsed.Host)
	if !strings.Contains(host, "lingma") && !strings.Contains(host, "rdc.aliyuncs.com") {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
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
