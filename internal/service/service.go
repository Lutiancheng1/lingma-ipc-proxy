package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lingma-ipc-proxy/internal/lingmaipc"
	"lingma-ipc-proxy/internal/remote"
	"lingma-ipc-proxy/internal/toolemulation"
)

type BackendMode string

const (
	BackendIPC    BackendMode = "ipc"
	BackendRemote BackendMode = "remote"
)

type SessionMode string

const (
	SessionModeAuto  SessionMode = "auto"
	SessionModeFresh SessionMode = "fresh"
	SessionModeReuse SessionMode = "reuse"
)

type Config struct {
	Host                  string
	Port                  int
	Backend               BackendMode
	Transport             lingmaipc.Transport
	Pipe                  string
	WebSocketURL          string
	RemoteBaseURL         string
	RemoteAuthFile        string
	RemoteVersion         string
	Cwd                   string
	CurrentFilePath       string
	Mode                  string
	Model                 string
	ShellType             string
	SessionMode           SessionMode
	Timeout               time.Duration
	RemoteFallbackEnabled bool
	RemoteFallbackModels  []string
}

type Image struct {
	MediaType string // e.g. "image/jpeg", "image/png"
	Data      string // base64 encoded data without prefix
	URL       string // optional original URL
}

type ChatMessage struct {
	Role   string
	Text   string
	Images []Image
}

type ChatRequest struct {
	Model             string
	System            string
	Messages          []ChatMessage
	Tools             []toolemulation.ToolDef
	ToolChoice        toolemulation.ToolChoice
	ParallelToolCalls *bool

	// Generation parameters (passed through for API compatibility;
	// actual effect depends on Lingma backend support)
	Temperature      *float64
	TopP             *float64
	TopK             int
	Stop             []string
	PresencePenalty  float64
	FrequencyPenalty float64
	MaxTokens        int
	Seed             int
	User             string
	ReasoningEffort  string
	ResponseFormat   string // "json" or "json_schema"
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
	Endpoint         string
	Transport        string
	EffectiveSession SessionMode
	ToolCalls        []toolemulation.ToolCall
}

type StreamEvent struct {
	Delta string
}

type StreamResult struct {
	Result *ChatResult
	Err    error
}

type Model struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Scene      string `json:"scene,omitempty"`
	InternalID string `json:"-"`
}

type State struct {
	PipePath        string      `json:"pipe_path,omitempty"`
	Endpoint        string      `json:"endpoint,omitempty"`
	Transport       string      `json:"transport,omitempty"`
	Connected       bool        `json:"connected"`
	StickySessionID string      `json:"sticky_session_id,omitempty"`
	SessionMode     SessionMode `json:"session_mode"`
}

type Service struct {
	cfg             Config
	mu              sync.Mutex
	client          *lingmaipc.Client
	pipePath        string
	endpoint        string
	transport       lingmaipc.Transport
	stickySessionID string
	stickyModelID   string
	modelMap        map[string]string // official name -> internal id
	remoteClient    *remote.Client
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
	cfg.Model = strings.TrimSpace(cfg.Model)
	if strings.TrimSpace(cfg.ShellType) == "" {
		cfg.ShellType = lingmaipc.DefaultShellType()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 300 * time.Second
	}
	if cfg.Transport == "" {
		cfg.Transport = lingmaipc.TransportAuto
	}
	if cfg.Backend == "" {
		cfg.Backend = BackendRemote
	}
	if cfg.Backend == BackendRemote {
		if len(cfg.RemoteFallbackModels) == 0 {
			cfg.RemoteFallbackModels = DefaultRemoteFallbackModels()
		}
	}
	cfg.Model = normalizeModelForBackend(cfg.Backend, cfg.Model)
	if cfg.SessionMode == "" {
		cfg.SessionMode = SessionModeAuto
	}
	return &Service{cfg: cfg}
}

func DefaultRemoteFallbackModels() []string {
	return []string{
		"kmodel",
		"mmodel",
		"dashscope_qwen3_coder",
		"dashscope_qmodel",
		"dashscope_qwen_max_latest",
		"dashscope_qwen_plus_20250428_thinking",
	}
}

func (s *Service) SetDefaultModel(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Model = normalizeModelForBackend(s.cfg.Backend, model)
}

func (s *Service) DefaultModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.cfg.Model)
}

func (s *Service) Warmup(ctx context.Context) error {
	if s.backend() == BackendRemote {
		return s.remoteClientLocked().Warmup(ctx)
	}
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
	if s.cfg.Backend == BackendRemote {
		return State{
			Endpoint:    remote.ResolveBaseURL(s.cfg.RemoteBaseURL),
			Transport:   "remote",
			Connected:   s.remoteClient != nil,
			SessionMode: s.cfg.SessionMode,
		}
	}
	return State{
		PipePath:        s.pipePath,
		Endpoint:        s.endpoint,
		Transport:       string(s.transport),
		Connected:       s.client != nil,
		StickySessionID: s.stickySessionID,
		SessionMode:     s.cfg.SessionMode,
	}
}

func (s *Service) ListModels(ctx context.Context) ([]Model, error) {
	if s.backend() == BackendRemote {
		models, err := s.remoteClientLocked().ListModels(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]Model, 0, len(models))
		seen := map[string]bool{}
		for _, model := range models {
			id := strings.TrimSpace(model.Key)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			name := strings.TrimSpace(model.DisplayName)
			if name == "" {
				name = id
			}
			out = append(out, Model{ID: id, Name: name})
		}
		return out, nil
	}

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

	s.mu.Lock()
	s.modelMap = make(map[string]string, len(models))
	for _, m := range models {
		if m.InternalID != "" {
			s.modelMap[m.ID] = m.InternalID
		}
	}
	s.mu.Unlock()

	return models, nil
}

func (s *Service) Generate(ctx context.Context, req ChatRequest) (*ChatResult, error) {
	if s.backend() == BackendRemote {
		return s.generateRemote(ctx, req, nil)
	}
	return s.generateWithReconnect(ctx, req, nil)
}

func (s *Service) GenerateStream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, <-chan StreamResult, error) {
	events := make(chan StreamEvent, 256)
	done := make(chan StreamResult, 1)

	go func() {
		generate := s.generateWithReconnect
		if s.backend() == BackendRemote {
			generate = s.generateRemote
		}
		result, err := generate(ctx, req, func(delta string) {
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

func (s *Service) generateWithReconnect(
	ctx context.Context,
	req ChatRequest,
	onDelta func(string),
) (*ChatResult, error) {
	result, err := s.generateLocked(ctx, req, onDelta)
	if err == nil || !isRecoverableIPCError(err) {
		return result, err
	}

	s.resetConnection()
	return s.generateLocked(ctx, req, onDelta)
}

func (s *Service) generateRemote(
	ctx context.Context,
	req ChatRequest,
	onDelta func(string),
) (*ChatResult, error) {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = s.DefaultModel()
	}
	req.Model = normalizeModelForBackend(BackendRemote, req.Model)
	prompt, err := buildLingmaPrompt(req, SessionModeFresh)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("empty user message")
	}

	models := s.remoteAttemptModels(ctx, req.Model)
	client := s.remoteClientLocked()
	var lastErr error
	for i, model := range models {
		attemptCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
		result, emitted, err := s.generateRemoteWithModel(attemptCtx, client, req, prompt, model, onDelta)
		cancel()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if i == len(models)-1 || emitted || !isRemoteFallbackError(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (s *Service) generateRemoteWithModel(
	ctx context.Context,
	client *remote.Client,
	req ChatRequest,
	prompt string,
	model string,
	onDelta func(string),
) (*ChatResult, bool, error) {
	emitted := false
	delta := func(text string) {
		if text != "" {
			emitted = true
		}
		if onDelta != nil {
			onDelta(text)
		}
	}
	remoteResult, err := client.Chat(ctx, remote.ChatRequest{
		Model:       model,
		Prompt:      prompt,
		Stream:      onDelta != nil,
		Temperature: req.Temperature,
	}, delta)
	if err != nil {
		return nil, emitted, err
	}

	result := &ChatResult{
		Text:             remoteResult.Text,
		Model:            valueOr(strings.TrimSpace(model), "lingma"),
		InputTokens:      remoteResult.InputTokens,
		OutputTokens:     remoteResult.OutputTokens,
		SessionID:        "",
		RequestID:        remoteResult.RequestID,
		FinishReason:     "stop",
		StopReason:       "stop",
		Endpoint:         remote.ResolveBaseURL(s.cfg.RemoteBaseURL),
		Transport:        "remote",
		EffectiveSession: SessionModeFresh,
	}
	s.applyToolEmulation(ctx, req, prompt, result, onDelta, func(hintPrompt string) (string, int, error) {
		retryResult, retryErr := client.Chat(ctx, remote.ChatRequest{
			Model:       model,
			Prompt:      hintPrompt,
			Stream:      onDelta != nil,
			Temperature: req.Temperature,
		}, onDelta)
		if retryErr != nil {
			return "", 0, retryErr
		}
		if retryResult == nil {
			return "", 0, nil
		}
		return retryResult.Text, retryResult.OutputTokens, nil
	})
	return result, emitted, nil
}

func (s *Service) remoteAttemptModels(ctx context.Context, primary string) []string {
	primary = normalizeModelForBackend(BackendRemote, primary)
	models := []string{primary}
	if !s.cfg.RemoteFallbackEnabled {
		return models
	}

	availableCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	remoteModels, err := s.remoteClientLocked().ListModels(availableCtx)
	cancel()
	if err != nil {
		return models
	}

	available := make(map[string]bool, len(remoteModels))
	for _, model := range remoteModels {
		key := normalizeModelForBackend(BackendRemote, model.Key)
		if key != "" {
			available[key] = true
		}
	}

	fallbackModels := s.cfg.RemoteFallbackModels
	if len(fallbackModels) == 0 {
		fallbackModels = DefaultRemoteFallbackModels()
	}
	ordered := make([]string, 0, len(fallbackModels))
	seen := map[string]bool{primary: true}
	primaryIndex := -1
	for _, candidate := range fallbackModels {
		model := normalizeModelForBackend(BackendRemote, candidate)
		if model == "" {
			continue
		}
		if model == primary && primaryIndex == -1 {
			primaryIndex = len(ordered)
		}
		ordered = append(ordered, model)
	}

	start := 0
	if primaryIndex >= 0 {
		start = primaryIndex + 1
	}
	for _, model := range ordered[start:] {
		if seen[model] || !available[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	return models
}

func isRemoteFallbackError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "client.timeout") ||
		strings.Contains(msg, "timeout awaiting response") ||
		strings.Contains(msg, "remote chat status 5") ||
		strings.Contains(msg, "remote chat status 429") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "unexpected eof")
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

	if strings.TrimSpace(req.Model) == "" {
		req.Model = s.DefaultModel()
	}
	internalModelID := s.resolveInternalModelID(req.Model)

	requestID := lingmaipc.CreateRequestID("serve")
	meta := lingmaipc.CreateMeta(lingmaipc.MetaOptions{
		RequestID:       requestID,
		Mode:            s.cfg.Mode,
		Model:           internalModelID,
		ShellType:       s.cfg.ShellType,
		CurrentFilePath: s.cfg.CurrentFilePath,
		EnabledMCP:      []any{},
	})

	modelID := strings.TrimSpace(internalModelID)
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

	images := extractLastUserImages(req.Messages)

	runResult, err := s.runPromptLocked(requestCtx, ipcClient, sessionID, prompt, images, requestID, meta, onDelta)
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

	s.applyToolEmulation(requestCtx, req, prompt, result, onDelta, func(hintPrompt string) (string, int, error) {
		retryRequestID := lingmaipc.CreateRequestID("serve-tool")
		retryMeta := lingmaipc.CreateMeta(lingmaipc.MetaOptions{
			RequestID:       retryRequestID,
			Mode:            s.cfg.Mode,
			Model:           internalModelID,
			ShellType:       s.cfg.ShellType,
			CurrentFilePath: s.cfg.CurrentFilePath,
			EnabledMCP:      []any{},
		})
		retryRunResult, retryErr := s.runPromptLocked(requestCtx, ipcClient, sessionID, hintPrompt, images, retryRequestID, retryMeta, onDelta)
		if retryErr != nil {
			return "", 0, retryErr
		}
		return retryRunResult.AssistantText, estimateTokens(retryRunResult.AssistantText), nil
	})
	return result, nil
}

func (s *Service) backend() BackendMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Backend == "" {
		return BackendIPC
	}
	return s.cfg.Backend
}

func (s *Service) remoteClientLocked() *remote.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteClient == nil {
		s.remoteClient = remote.New(remote.Config{
			BaseURL:     s.cfg.RemoteBaseURL,
			AuthFile:    s.cfg.RemoteAuthFile,
			CosyVersion: s.cfg.RemoteVersion,
			Timeout:     s.cfg.Timeout,
		})
	}
	return s.remoteClient
}

func (s *Service) applyToolEmulation(
	ctx context.Context,
	req ChatRequest,
	prompt string,
	result *ChatResult,
	onDelta func(string),
	retry func(string) (string, int, error),
) {
	if len(req.Tools) > 0 {
		calls, remaining, parseErr := toolemulation.ParseActionBlocks(result.Text, req.Tools, toolemulation.Config{})
		if parseErr == nil && len(calls) > 0 {
			result.Text = remaining
			result.ToolCalls = calls
		} else if shouldRetryTooling(req.ToolChoice, result.Text) {
			hintPrompt := prompt + "\n\n" + toolemulation.ForceToolingPrompt(req.ToolChoice)
			retryText := ""
			if retry != nil {
				text, outputTokens, retryErr := retry(hintPrompt)
				if retryErr == nil {
					retryText = text
					if outputTokens > 0 {
						result.OutputTokens = outputTokens
					}
				}
			}
			if retryText != "" {
				retryCalls, retryRemaining, retryParseErr := toolemulation.ParseActionBlocks(retryText, req.Tools, toolemulation.Config{})
				if retryParseErr == nil && len(retryCalls) > 0 {
					result.Text = retryRemaining
					result.ToolCalls = retryCalls
					result.OutputTokens = estimateTokens(retryText)
				} else if inferred := toolemulation.InferToolCallsFromText(retryText, req.Tools); len(inferred) > 0 {
					result.Text = ""
					result.ToolCalls = inferred
					result.OutputTokens = estimateTokens(retryText)
				}
			}
			if len(result.ToolCalls) == 0 {
				if inferred := toolemulation.InferToolCallsFromText(result.Text, req.Tools); len(inferred) > 0 {
					result.Text = ""
					result.ToolCalls = inferred
				}
			}
		}
	}
}

func shouldRetryTooling(choice toolemulation.ToolChoice, text string) bool {
	switch choice.Mode {
	case "any", "tool":
		return true
	case "none":
		return false
	}
	return toolemulation.LooksLikeRefusal(text) || toolemulation.LooksLikeMissedToolUse(text)
}

func isRecoverableIPCError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	needles := []string{
		"use of closed network connection",
		"broken pipe",
		"connection reset by peer",
		"connection refused",
		"websocket: close",
		"unexpected eof",
		"io: read/write on closed pipe",
		"lingma ipc notification stream closed",
	}
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func (s *Service) buildChatResult(
	req ChatRequest,
	sessionID string,
	requestID string,
	prompt string,
	runResult *promptRunResult,
	effectiveMode SessionMode,
) *ChatResult {
	endpoint := s.currentPipePath()
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
		PipePath:         endpoint,
		Endpoint:         endpoint,
		Transport:        string(s.currentTransport()),
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

	dialOptions, err := lingmaipc.ResolveDialOptions(s.cfg.Transport, s.cfg.Pipe, s.cfg.WebSocketURL)
	if err != nil {
		return nil, err
	}
	client, err := lingmaipc.Connect(ctx, dialOptions)
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
	s.pipePath = dialOptions.PipePath
	s.endpoint = client.Address()
	s.transport = client.Transport()
	return client, nil
}

func (s *Service) closeClientLocked() error {
	if s.client == nil {
		s.pipePath = ""
		s.endpoint = ""
		s.transport = ""
		s.clearStickyLocked()
		return nil
	}
	client := s.client
	s.client = nil
	s.pipePath = ""
	s.endpoint = ""
	s.transport = ""
	s.clearStickyLocked()
	return client.Close()
}

func (s *Service) resetConnection() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.closeClientLocked()
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
	if strings.TrimSpace(s.endpoint) != "" {
		return s.endpoint
	}
	return s.pipePath
}

func (s *Service) currentTransport() lingmaipc.Transport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transport
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
	images []Image,
	requestID string,
	meta map[string]any,
	onDelta func(string),
) (*promptRunResult, error) {
	notifications, cancel := client.Subscribe()
	defer cancel()

	promptItems := []map[string]any{
		{"type": "text", "text": text},
	}

	// Build contextParams for images using Lingma's native format
	var contextParams []map[string]any
	for _, img := range images {
		if img.Data == "" && img.URL == "" {
			continue
		}
		mediaType := img.MediaType
		if mediaType == "" {
			mediaType = "image/jpeg"
		}

		// Determine file extension from mediaType
		ext := "jpg"
		switch mediaType {
		case "image/png":
			ext = "png"
		case "image/gif":
			ext = "gif"
		case "image/webp":
			ext = "webp"
		case "image/bmp":
			ext = "bmp"
		}

		// If we have base64 data, save to temp file and build lingma URI
		var imageURI string
		if img.Data != "" {
			tmpFile, err := os.CreateTemp("", "lingma-img-*"+"."+ext)
			if err == nil {
				data, _ := base64.StdEncoding.DecodeString(img.Data)
				if len(data) > 0 {
					_ = os.WriteFile(tmpFile.Name(), data, 0644)
					absPath, _ := filepath.Abs(tmpFile.Name())
					imageURI = "lingma:///agent/file?path=" + url.QueryEscape(absPath)
				}
				tmpFile.Close()
			}
		}
		if imageURI == "" && img.URL != "" {
			imageURI = img.URL
		}

		// Add to promptItems using Lingma native image format
		itemPrompt := map[string]any{
			"type":     "image",
			"mimeType": mediaType,
		}
		if imageURI != "" {
			itemPrompt["uri"] = imageURI
		}
		if img.Data != "" {
			itemPrompt["data"] = img.Data
		}
		promptItems = append(promptItems, itemPrompt)

		// Add to contextParams using Lingma native format
		item := map[string]any{
			"type":     "image",
			"mimeType": mediaType,
		}
		if imageURI != "" {
			item["uri"] = imageURI
		}
		if img.Data != "" {
			item["data"] = img.Data
		}
		contextParams = append(contextParams, item)
	}

	params := map[string]any{
		"sessionId":     sessionID,
		"prompt":        promptItems,
		"contextParams": contextParams,
		"_meta":         meta,
	}
	// Fallback: if images have URLs, also pass via extra field
	for _, img := range images {
		if img.URL != "" {
			params["extra"] = map[string]any{"imageUrl": img.URL}
			break
		}
	}

	if err := client.Send("session/prompt", params); err != nil {
		return nil, err
	}

	result := &promptRunResult{PromptResult: map[string]any{}}
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
	return SessionModeFresh
}

func extractLastUserImages(messages []ChatMessage) []Image {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Images
		}
	}
	return nil
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

	system := strings.TrimSpace(req.System)
	if len(req.Tools) > 0 && req.ToolChoice.Mode != "none" {
		system = toolemulation.InjectTooling(system, req.Tools, req.ToolChoice, req.ParallelToolCalls)
	}

	if system == "" && len(messages) == 1 {
		return lastUser, nil
	}

	if len(req.Tools) > 0 {
		parts := make([]string, 0, len(messages)+3)
		for _, message := range messages {
			role := "User"
			if message.Role == "assistant" {
				role = "Assistant"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", role, message.Text))
		}
		if system != "" {
			// Append tool prompt right before the final "Assistant:" so it
			// is the last thing the model sees before generating a reply.
			parts = append(parts, system)
		}
		parts = append(parts, "Assistant:")
		return strings.Join(parts, "\n\n"), nil
	}

	parts := make([]string, 0, len(messages)+4)
	if system != "" {
		parts = append(parts, "System instructions:", system)
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
				seen[name] = Model{ID: name, Name: name, Scene: currentScene, InternalID: id}
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

func (s *Service) resolveInternalModelID(officialName string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if internalID, ok := s.modelMap[officialName]; ok && internalID != "" {
		return internalID
	}
	return officialName
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

func normalizeModelForBackend(backend BackendMode, model string) string {
	model = strings.TrimSpace(model)
	if backend != BackendRemote {
		return model
	}
	switch strings.ToLower(model) {
	case "":
		return ""
	case "kimi-k2.6":
		return "kmodel"
	case "minimax-m2.7":
		return "mmodel"
	case "qwen3-coder":
		return "dashscope_qwen3_coder"
	case "qwen3-max":
		return "dashscope_qwen_max_latest"
	case "qwen3-thinking":
		return "dashscope_qwen_plus_20250428_thinking"
	case "qwen3.6-plus":
		return "dashscope_qmodel"
	case "auto":
		return "org_auto"
	default:
		return model
	}
}
