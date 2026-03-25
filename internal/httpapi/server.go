package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lingma-ipc-proxy/internal/service"
)

type Server struct {
	svc  *service.Service
	http *http.Server
	sem  chan struct{}
}

type anthropicRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens,omitempty"`
	System    any          `json:"system,omitempty"`
	Messages  []rawMessage `json:"messages"`
	Stream    bool         `json:"stream,omitempty"`
}

type openAIChatRequest struct {
	Model               string       `json:"model"`
	Messages            []rawMessage `json:"messages"`
	Stream              bool         `json:"stream,omitempty"`
	MaxTokens           int          `json:"max_tokens,omitempty"`
	MaxCompletionTokens int          `json:"max_completion_tokens,omitempty"`
}

type rawMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type modelResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func NewServer(addr string, svc *service.Service) *Server {
	s := &Server{
		svc: svc,
		sem: make(chan struct{}, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/health", s.handleRoot)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAIChatCompletions)

	s.http = &http.Server{
		Addr:              addr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe() error {
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	err := s.http.Shutdown(ctx)
	closeErr := s.svc.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/health" {
		writeOpenAIError(w, http.StatusNotFound, "not_found_error", "not found")
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "lingma-ipc-proxy",
		"state":   s.svc.State(),
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	models, err := s.svc.ListModels(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	data := make([]modelResponse, 0, len(models))
	created := time.Now().Unix()
	for _, model := range models {
		data = append(data, modelResponse{
			ID:      model.ID,
			Object:  "model",
			Created: created,
			OwnedBy: "lingma",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	if !s.tryAcquire() {
		writeAnthropicError(w, http.StatusTooManyRequests, "rate_limit_error", "Lingma IPC proxy handles one request at a time.")
		return
	}
	defer s.release()

	var req anthropicRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	normalized, err := normalizeAnthropicRequest(req)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if req.Stream {
		s.handleAnthropicStream(w, r, normalized)
		return
	}

	result, err := s.svc.Generate(r.Context(), normalized)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":            fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		"type":          "message",
		"role":          "assistant",
		"content":       []map[string]any{{"type": "text", "text": result.Text}},
		"model":         result.Model,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  result.InputTokens,
			"output_tokens": result.OutputTokens,
		},
	})
}

func (s *Server) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	if !s.tryAcquire() {
		writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_error", "Lingma IPC proxy handles one request at a time.")
		return
	}
	defer s.release()

	var req openAIChatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	normalized, err := normalizeOpenAIRequest(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if req.Stream {
		s.handleOpenAIStream(w, r, normalized)
		return
	}

	result, err := s.svc.Generate(r.Context(), normalized)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	created := time.Now().Unix()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": created,
		"model":   result.Model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": result.Text,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     result.InputTokens,
			"completion_tokens": result.OutputTokens,
			"total_tokens":      result.InputTokens + result.OutputTokens,
		},
	})
}

func (s *Server) handleAnthropicStream(w http.ResponseWriter, r *http.Request, req service.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "streaming is not supported by this server")
		return
	}

	events, done, err := s.svc.GenerateStream(r.Context(), req)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "lingma"
	}
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	streamingHeaders(w)
	if err := writeSSEEvent(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	}); err != nil {
		return
	}
	if err := writeSSEEvent(w, flusher, "content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}); err != nil {
		return
	}

	eventsCh := events
	doneCh := done
	var final *service.ChatResult
	var finalErr error

	for eventsCh != nil || doneCh != nil {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				continue
			}
			if event.Delta == "" {
				continue
			}
			if err := writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type": "text_delta",
					"text": event.Delta,
				},
			}); err != nil {
				return
			}
		case result, ok := <-doneCh:
			if !ok {
				doneCh = nil
				continue
			}
			final = result.Result
			finalErr = result.Err
			doneCh = nil
		}
	}

	if finalErr != nil {
		_ = writeSSEEvent(w, flusher, "error", map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "api_error",
				"message": finalErr.Error(),
			},
		})
		return
	}
	if final == nil {
		_ = writeSSEEvent(w, flusher, "error", map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "api_error",
				"message": "stream finished without a final result",
			},
		})
		return
	}
	if err := writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	}); err != nil {
		return
	}
	if err := writeSSEEvent(w, flusher, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": final.OutputTokens,
		},
	}); err != nil {
		return
	}
	_ = writeSSEEvent(w, flusher, "message_stop", map[string]any{
		"type": "message_stop",
	})
}

func (s *Server) handleOpenAIStream(w http.ResponseWriter, r *http.Request, req service.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "streaming is not supported by this server")
		return
	}

	events, done, err := s.svc.GenerateStream(r.Context(), req)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "lingma"
	}
	chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	streamingHeaders(w)
	if err := writeOpenAIChunk(w, flusher, map[string]any{
		"id":      chatID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{
					"role": "assistant",
				},
				"finish_reason": nil,
			},
		},
	}); err != nil {
		return
	}

	eventsCh := events
	doneCh := done
	var finalErr error

	for eventsCh != nil || doneCh != nil {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				continue
			}
			if event.Delta == "" {
				continue
			}
			if err := writeOpenAIChunk(w, flusher, map[string]any{
				"id":      chatID,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   model,
				"choices": []map[string]any{
					{
						"index": 0,
						"delta": map[string]any{
							"content": event.Delta,
						},
						"finish_reason": nil,
					},
				},
			}); err != nil {
				return
			}
		case result, ok := <-doneCh:
			if !ok {
				doneCh = nil
				continue
			}
			finalErr = result.Err
			doneCh = nil
		}
	}

	if finalErr != nil {
		_ = writeOpenAIChunk(w, flusher, map[string]any{
			"error": map[string]any{
				"message": finalErr.Error(),
				"type":    "api_error",
				"code":    nil,
				"param":   nil,
			},
		})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	if err := writeOpenAIChunk(w, flusher, map[string]any{
		"id":      chatID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "stop",
			},
		},
	}); err != nil {
		return
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func normalizeAnthropicRequest(req anthropicRequest) (service.ChatRequest, error) {
	messages := make([]service.ChatMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		text := strings.TrimSpace(extractText(message.Content))
		if role != "user" && role != "assistant" {
			continue
		}
		if text == "" {
			continue
		}
		messages = append(messages, service.ChatMessage{Role: role, Text: text})
	}
	if len(messages) == 0 {
		return service.ChatRequest{}, fmt.Errorf("no user or assistant messages found")
	}
	return service.ChatRequest{
		Model:    strings.TrimSpace(req.Model),
		System:   strings.TrimSpace(extractText(req.System)),
		Messages: messages,
	}, nil
}

func normalizeOpenAIRequest(req openAIChatRequest) (service.ChatRequest, error) {
	messages := make([]service.ChatMessage, 0, len(req.Messages))
	systemParts := make([]string, 0, 2)
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		text := strings.TrimSpace(extractText(message.Content))
		if text == "" {
			continue
		}
		switch role {
		case "system":
			systemParts = append(systemParts, text)
		case "user", "assistant":
			messages = append(messages, service.ChatMessage{Role: role, Text: text})
		}
	}
	if len(messages) == 0 {
		return service.ChatRequest{}, fmt.Errorf("no user or assistant messages found")
	}
	return service.ChatRequest{
		Model:    strings.TrimSpace(req.Model),
		System:   strings.Join(systemParts, "\n\n"),
		Messages: messages,
	}, nil
}

func extractText(content any) string {
	switch typed := content.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			text := extractText(item)
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text := stringFromAny(typed["text"]); text != "" {
			return text
		}
		if text := stringFromAny(typed["input_text"]); text != "" {
			return text
		}
		if nested := extractText(typed["content"]); nested != "" {
			return nested
		}
		return ""
	default:
		return ""
	}
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeAnthropicError(w http.ResponseWriter, status int, kind string, message string) {
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    kind,
			"message": message,
		},
	})
}

func writeOpenAIError(w http.ResponseWriter, status int, kind string, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    kind,
			"code":    nil,
			"param":   nil,
		},
	})
}

func streamingHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeOpenAIChunk(w http.ResponseWriter, flusher http.Flusher, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) tryAcquire() bool {
	select {
	case s.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) release() {
	select {
	case <-s.sem:
	default:
	}
}
