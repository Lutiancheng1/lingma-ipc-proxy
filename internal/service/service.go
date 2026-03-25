package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"lingma-ipc-proxy/internal/lingmaipc"
)

type SessionMode string

const (
	SessionModeAuto  SessionMode = "auto"
	SessionModeFresh SessionMode = "fresh"
	SessionModeReuse SessionMode = "reuse"
)

type Config struct {
	Host            string
	Port            int
	Pipe            string
	Cwd             string
	CurrentFilePath string
	Mode            string
	ShellType       string
	SessionMode     SessionMode
	Timeout         time.Duration
}

type ChatMessage struct {
	Role string
	Text string
}

type ChatRequest struct {
	Model    string
	System   string
	Messages []ChatMessage
}

type ChatResult struct {
	Text             string
	Model            string
	InputTokens      int
	OutputTokens     int
	SessionID        string
	RequestID        string
	FinishReason     string
	StopReason       string
	UsedTokens       int
	LimitTokens      int
	PipePath         string
	EffectiveSession SessionMode
}

type StreamEvent struct {
	Delta string
}

type StreamResult struct {
	Result *ChatResult
	Err    error
}

type Model struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scene string `json:"scene,omitempty"`
}

type State struct {
	PipePath        string      `json:"pipe_path,omitempty"`
	Connected       bool        `json:"connected"`
	StickySessionID string      `json:"sticky_session_id,omitempty"`
	SessionMode     SessionMode `json:"session_mode"`
}

type Service struct {
	cfg             Config
	mu              sync.Mutex
	client          *lingmaipc.Client
	pipePath        string
	stickySessionID string
	stickyModelID   string
}

type promptRunResult struct {
	PromptResult  map[string]any
	FinishData    map[string]any
	ContextUsage  map[string]any
	AssistantText string
	TimedOut      bool
}

func New(cfg Config) *Service {
	if strings.TrimSpace(cfg.Cwd) == "" {
		if wd, err := os.Getwd(); err == nil {
			cfg.Cwd = wd
		}
	}
	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = "agent"
	}
	if strings.TrimSpace(cfg.ShellType) == "" {
		cfg.ShellType = lingmaipc.DefaultShellType()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}
	if cfg.SessionMode == "" {
		cfg.SessionMode = SessionModeAuto
	}
	return &Service{cfg: cfg}
}

func (s *Service) Warmup(ctx context.Context) error {
	_, err := s.ensureConnected(ctx)
	return err
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeClientLocked()
}

func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return State{
		PipePath:        s.pipePath,
		Connected:       s.client != nil,
		StickySessionID: s.stickySessionID,
		SessionMode:     s.cfg.SessionMode,
	}
}

func (s *Service) ListModels(ctx context.Context) ([]Model, error) {
	ipcClient, err := s.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}

	var raw any
	if err := ipcClient.Request(ctx, "config/queryModels", map[string]any{}, &raw); err != nil {
		return nil, err
	}

	models := extractModels(raw)
	if len(models) == 0 {
		models = []Model{{ID: "lingma", Name: "Lingma", Scene: "default"}}
	}
	return models, nil
}

func (s *Service) Generate(ctx context.Context, req ChatRequest) (*ChatResult, error) {
	return s.generateLocked(ctx, req, nil)
}

func (s *Service) GenerateStream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, <-chan StreamResult, error) {
	events := make(chan StreamEvent, 256)
	done := make(chan StreamResult, 1)

	go func() {
		result, err := s.generateLocked(ctx, req, func(delta string) {
			if delta == "" {
				return
			}
			select {
			case events <- StreamEvent{Delta: delta}:
			case <-ctx.Done():
			}
		})

		close(events)
		done <- StreamResult{Result: result, Err: err}
		close(done)
	}()

	return events, done, nil
}

func (s *Service) generateLocked(
	ctx context.Context,
	req ChatRequest,
	onDelta func(string),
) (result *ChatResult, err error) {
	requestCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	ipcClient, err := s.ensureConnected(requestCtx)
	if err != nil {
		return nil, err
	}

	effectiveMode := resolveSessionMode(req, s.cfg.SessionMode)
	prompt, err := buildLingmaPrompt(req, effectiveMode)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("empty user message")
	}

	sessionID, err := s.resolveSession(requestCtx, ipcClient, effectiveMode)
	if err != nil {
		return nil, err
	}
	defer func() {
		if effectiveMode == SessionModeReuse || strings.TrimSpace(sessionID) == "" {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = s.deleteSessionLocked(cleanupCtx, ipcClient, sessionID)
	}()

	requestID := lingmaipc.CreateRequestID("serve")
	meta := lingmaipc.CreateMeta(lingmaipc.MetaOptions{
		RequestID:       requestID,
		Mode:            s.cfg.Mode,
		Model:           req.Model,
		ShellType:       s.cfg.ShellType,
		CurrentFilePath: s.cfg.CurrentFilePath,
		EnabledMCP:      []any{},
	})

	modelID := strings.TrimSpace(req.Model)
	if modelID != "" && s.shouldSetModel(sessionID, effectiveMode, modelID) {
		if err := ipcClient.Request(requestCtx, "session/set_model", map[string]any{
			"sessionId": sessionID,
			"modelId":   modelID,
			"timestamp": time.Now().UnixMilli(),
			"_meta":     meta,
		}, nil); err != nil {
			if effectiveMode == SessionModeReuse {
				s.invalidateStickySession()
			}
			return nil, err
		}
		s.rememberStickyModel(sessionID, modelID)
	}

	runResult, err := s.runPromptLocked(requestCtx, ipcClient, sessionID, prompt, requestID, meta, onDelta)
	if err != nil {
		if effectiveMode == SessionModeReuse {
			s.invalidateStickySession()
		}
		return nil, err
	}
	if runResult.TimedOut || strings.TrimSpace(runResult.AssistantText) == "" {
		if effectiveMode == SessionModeReuse {
			s.invalidateStickySession()
		}
	}
	if runResult.TimedOut && strings.TrimSpace(runResult.AssistantText) == "" {
		return nil, errors.New("timed out while waiting for Lingma IPC to finish responding")
	}
	if strings.TrimSpace(runResult.AssistantText) == "" {
		return nil, errors.New("Lingma IPC did not produce an assistant reply")
	}
	if runResult.TimedOut {
		return nil, fmt.Errorf("Lingma IPC response remained incomplete before timeout. Partial reply: %s", truncate(runResult.AssistantText, 120))
	}

	result = s.buildChatResult(req, sessionID, requestID, prompt, runResult, effectiveMode)
	return result, nil
}

func (s *Service) buildChatResult(
	req ChatRequest,
	sessionID string,
	requestID string,
	prompt string,
	runResult *promptRunResult,
	effectiveMode SessionMode,
) *ChatResult {
	return &ChatResult{
		Text:             runResult.AssistantText,
		Model:            valueOr(strings.TrimSpace(req.Model), "lingma"),
		InputTokens:      estimateTokens(prompt),
		OutputTokens:     estimateTokens(runResult.AssistantText),
		SessionID:        sessionID,
		RequestID:        requestID,
		FinishReason:     nestedString(runResult.FinishData, "reason"),
		StopReason:       nestedString(runResult.PromptResult, "stopReason"),
		UsedTokens:       int(nestedInt64(runResult.ContextUsage, "usedTokens")),
		LimitTokens:      int(nestedInt64(runResult.ContextUsage, "limitTokens")),
		PipePath:         s.currentPipePath(),
		EffectiveSession: effectiveMode,
	}
}

func (s *Service) ensureConnected(ctx context.Context) (*lingmaipc.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureConnectedLocked(ctx)
}

func (s *Service) ensureConnectedLocked(ctx context.Context) (*lingmaipc.Client, error) {
	if s.client != nil {
		return s.client, nil
	}

	pipePath, err := lingmaipc.ResolvePipePath(s.cfg.Pipe)
	if err != nil {
		return nil, err
	}
	client, err := lingmaipc.Connect(ctx, pipePath)
	if err != nil {
		return nil, err
	}
	if err := client.Request(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"timestamp":          time.Now().UnixMilli(),
	}, nil); err != nil {
		_ = client.Close()
		return nil, err
	}

	s.client = client
	s.pipePath = pipePath
	return client, nil
}

func (s *Service) closeClientLocked() error {
	if s.client == nil {
		s.pipePath = ""
		s.clearStickyLocked()
		return nil
	}
	client := s.client
	s.client = nil
	s.pipePath = ""
	s.clearStickyLocked()
	return client.Close()
}

func (s *Service) resolveSession(ctx context.Context, client *lingmaipc.Client, mode SessionMode) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolveSessionLocked(ctx, client, mode)
}

func (s *Service) invalidateStickySession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearStickyLocked()
}

func (s *Service) rememberStickyModel(sessionID string, modelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.stickySessionID) == strings.TrimSpace(sessionID) {
		s.stickyModelID = strings.TrimSpace(modelID)
	}
}

func (s *Service) shouldSetModel(sessionID string, mode SessionMode, modelID string) bool {
	if strings.TrimSpace(modelID) == "" {
		return false
	}
	if mode != SessionModeReuse {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.stickySessionID) != strings.TrimSpace(sessionID) {
		return true
	}
	return strings.TrimSpace(s.stickyModelID) != strings.TrimSpace(modelID)
}

func (s *Service) clearStickyLocked() {
	s.stickySessionID = ""
	s.stickyModelID = ""
}

func (s *Service) currentPipePath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pipePath
}

func (s *Service) resolveSessionLocked(ctx context.Context, client *lingmaipc.Client, mode SessionMode) (string, error) {
	if mode == SessionModeReuse && strings.TrimSpace(s.stickySessionID) != "" {
		return s.stickySessionID, nil
	}

	var created struct {
		SessionID string `json:"sessionId"`
		ID        string `json:"id"`
	}
	if err := client.Request(ctx, "session/new", map[string]any{
		"cwd":        s.cfg.Cwd,
		"mcpServers": []any{},
		"_meta":      map[string]any{},
		"timestamp":  time.Now().UnixMilli(),
	}, &created); err != nil {
		return "", err
	}

	sessionID := strings.TrimSpace(created.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(created.ID)
	}
	if sessionID == "" {
		return "", errors.New("Lingma IPC did not return a sessionId")
	}

	if mode == SessionModeReuse {
		s.stickySessionID = sessionID
		s.stickyModelID = ""
	}
	return sessionID, nil
}

func (s *Service) runPromptLocked(
	ctx context.Context,
	client *lingmaipc.Client,
	sessionID string,
	text string,
	requestID string,
	meta map[string]any,
	onDelta func(string),
) (*promptRunResult, error) {
	notifications, cancel := client.Subscribe()
	defer cancel()

	promptResult := map[string]any{}
	if err := client.Request(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{
			{"type": "text", "text": text},
		},
		"_meta": meta,
	}, &promptResult); err != nil {
		return nil, err
	}

	result := &promptRunResult{PromptResult: promptResult}
	var builder strings.Builder

	for {
		select {
		case <-ctx.Done():
			result.AssistantText = builder.String()
			result.TimedOut = true
			return result, nil
		case notification, ok := <-notifications:
			if !ok {
				result.AssistantText = builder.String()
				if result.AssistantText == "" {
					return nil, errors.New("Lingma IPC notification stream closed")
				}
				return result, nil
			}
			if notification.Method != "session/update" {
				continue
			}
			if nestedStringFromMap(notification.Params, "_meta", lingmaipc.MetaRequestID) != requestID {
				continue
			}

			update := nestedMap(notification.Params, "update")
			switch nestedString(update, "sessionUpdate") {
			case "agent_message_chunk":
				chunk := nestedString(nestedMap(update, "content"), "text")
				if chunk != "" {
					builder.WriteString(chunk)
					if onDelta != nil {
						onDelta(chunk)
					}
				}
			case "notification":
				switch nestedString(update, "type") {
				case "context_usage":
					result.ContextUsage = nestedMap(update, "data")
				case "chat_finish":
					result.FinishData = nestedMap(update, "data")
					result.AssistantText = builder.String()
					return result, nil
				}
			}
		}
	}
}

func (s *Service) deleteSessionLocked(ctx context.Context, client *lingmaipc.Client, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}

	if err := client.Request(ctx, "chat/deleteSessionById", map[string]any{
		"sessionId": sessionID,
	}, nil); err == nil {
		return nil
	}

	return client.Request(ctx, "chat/deleteSessionById", map[string]any{
		"id": sessionID,
	}, nil)
}

func resolveSessionMode(req ChatRequest, configured SessionMode) SessionMode {
	if configured != SessionModeAuto {
		return configured
	}
	if strings.TrimSpace(req.System) != "" || len(filteredMessages(req.Messages)) > 1 {
		return SessionModeFresh
	}
	return SessionModeReuse
}

func buildLingmaPrompt(req ChatRequest, mode SessionMode) (string, error) {
	messages := filteredMessages(req.Messages)
	var lastUser string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUser = messages[i].Text
			break
		}
	}
	if strings.TrimSpace(lastUser) == "" {
		return "", errors.New("no user message found in request")
	}
	if mode == SessionModeReuse {
		return lastUser, nil
	}
	if strings.TrimSpace(req.System) == "" && len(messages) == 1 {
		return lastUser, nil
	}

	parts := make([]string, 0, len(messages)+4)
	if strings.TrimSpace(req.System) != "" {
		parts = append(parts, "System instructions:", strings.TrimSpace(req.System))
	}
	parts = append(parts, "Conversation transcript:")
	for _, message := range messages {
		role := "User"
		if message.Role == "assistant" {
			role = "Assistant"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", role, message.Text))
	}
	parts = append(parts, "Reply as the assistant to the latest user message only. Follow the system instructions and prior transcript naturally.")
	return strings.Join(parts, "\n\n"), nil
}

func filteredMessages(messages []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		if role != "user" && role != "assistant" {
			continue
		}
		out = append(out, ChatMessage{Role: role, Text: text})
	}
	return out
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 1
	}
	return max(1, (len([]rune(text))+2)/3)
}

func extractModels(raw any) []Model {
	seen := make(map[string]Model)
	var walk func(scene string, value any)
	walk = func(scene string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			id := firstString(typed, "id", "modelId", "key")
			name := firstString(typed, "name", "label", "displayName", "title")
			currentScene := scene
			if currentScene == "" {
				currentScene = firstString(typed, "scene", "sceneId", "category")
			}
			if id != "" && (name != "" || likelyModelID(id)) {
				if name == "" {
					name = id
				}
				seen[id] = Model{ID: id, Name: name, Scene: currentScene}
			}
			for key, child := range typed {
				nextScene := currentScene
				if nextScene == "" || isSceneKey(key) {
					nextScene = key
				}
				walk(nextScene, child)
			}
		case []any:
			for _, item := range typed {
				walk(scene, item)
			}
		}
	}
	walk("", raw)

	models := make([]Model, 0, len(seen))
	for _, model := range seen {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func likelyModelID(id string) bool {
	lowered := strings.ToLower(id)
	return strings.Contains(lowered, "qwen") || strings.Contains(lowered, "model") || strings.Contains(lowered, "auto") || strings.Contains(lowered, "coder")
}

func isSceneKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "assistant", "chat", "developer", "inline", "quest":
		return true
	default:
		return false
	}
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			case json.Number:
				return typed.String()
			}
		}
	}
	return ""
}

func nestedMap(m map[string]any, key string) map[string]any {
	if value, ok := m[key]; ok {
		if typed, ok := value.(map[string]any); ok {
			return typed
		}
	}
	return map[string]any{}
}

func nestedString(m map[string]any, key string) string {
	if value, ok := m[key]; ok {
		switch typed := value.(type) {
		case string:
			return typed
		case json.Number:
			return typed.String()
		case float64:
			return fmt.Sprintf("%.0f", typed)
		}
	}
	return ""
}

func nestedStringFromMap(m map[string]any, parent string, key string) string {
	child := nestedMap(m, parent)
	return nestedString(child, key)
}

func nestedInt64(m map[string]any, key string) int64 {
	if value, ok := m[key]; ok {
		switch typed := value.(type) {
		case int:
			return int64(typed)
		case int64:
			return typed
		case float64:
			return int64(typed)
		case json.Number:
			if n, err := typed.Int64(); err == nil {
				return n
			}
		}
	}
	return 0
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
