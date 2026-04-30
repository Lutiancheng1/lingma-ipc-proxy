package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"lingma-ipc-proxy/internal/httpapi"
	"lingma-ipc-proxy/internal/lingmaipc"
	"lingma-ipc-proxy/internal/remote"
	"lingma-ipc-proxy/internal/service"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
// RequestRecord stores a single HTTP request summary
type RequestRecord struct {
	Time         string `json:"time"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	Model        string `json:"model,omitempty"`
	StatusCode   int    `json:"statusCode"`
	Duration     string `json:"duration"`
	Size         string `json:"size,omitempty"`
	InputTokens  int    `json:"inputTokens,omitempty"`
	OutputTokens int    `json:"outputTokens,omitempty"`
	TotalTokens  int    `json:"totalTokens,omitempty"`
	ReqBody      string `json:"reqBody,omitempty"`
	RespBody     string `json:"respBody,omitempty"`
}

type AppLog struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type TokenStats struct {
	TotalRequests   int            `json:"totalRequests"`
	SuccessRequests int            `json:"successRequests"`
	InputTokens     int            `json:"inputTokens"`
	OutputTokens    int            `json:"outputTokens"`
	TotalTokens     int            `json:"totalTokens"`
	ByModel         map[string]int `json:"byModel,omitempty"`
	LastModel       string         `json:"lastModel,omitempty"`
	LastUpdated     string         `json:"lastUpdated,omitempty"`
}

type App struct {
	ctx context.Context

	mu        sync.RWMutex
	cfg       service.Config
	server    *httpapi.Server
	running   bool
	quitting  bool
	addr      string
	startedAt time.Time
	quitHint  time.Time
	models    []ModelInfo
	requests  []RequestRecord
	logs      []AppLog
	stats     TokenStats
}

// ModelInfo represents a model returned by /v1/models
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProxyStatus represents the current proxy status
type ProxyStatus struct {
	Running   bool   `json:"running"`
	Addr      string `json:"addr"`
	Backend   string `json:"backend"`
	Models    int    `json:"models"`
	Model     string `json:"model,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
}

// DetectionInfo exposes non-sensitive resolved connection details for the UI.
type DetectionInfo struct {
	ListenURL               string `json:"listenUrl"`
	Backend                 string `json:"backend"`
	BackendLabel            string `json:"backendLabel"`
	IPCSuccess              bool   `json:"ipcSuccess"`
	IPCTransport            string `json:"ipcTransport,omitempty"`
	IPCEndpoint             string `json:"ipcEndpoint,omitempty"`
	IPCError                string `json:"ipcError,omitempty"`
	RemoteBaseURL           string `json:"remoteBaseUrl"`
	RemoteBaseURLSource     string `json:"remoteBaseUrlSource,omitempty"`
	RemoteCredentialSuccess bool   `json:"remoteCredentialSuccess"`
	RemoteCredentialSource  string `json:"remoteCredentialSource,omitempty"`
	RemoteUserID            string `json:"remoteUserId,omitempty"`
	RemoteMachineID         string `json:"remoteMachineId,omitempty"`
	RemoteTokenExpireAt     string `json:"remoteTokenExpireAt,omitempty"`
	RemoteTokenExpired      bool   `json:"remoteTokenExpired"`
	RemoteCredentialError   string `json:"remoteCredentialError,omitempty"`
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.cfg = defaultConfig()
	if err := a.loadAppState(); err != nil {
		runtime.LogWarningf(a.ctx, "failed to load app state: %v", err)
	}

	// Auto-save default config on first run so users can find/edit it later
	if err := a.saveConfig(a.cfg); err != nil {
		runtime.LogWarningf(a.ctx, "failed to save default config: %v", err)
	}

	// Auto-start proxy so the app is usable immediately
	go func() {
		if err := a.StartProxy(); err != nil {
			a.emitLog("error", fmt.Sprintf("Auto-start failed: %v. %s", err, transportFallbackHint()))
		} else {
			a.emitLog("info", "Proxy auto-started")
		}
	}()
}

// onDomReady is called when the frontend DOM is ready
func (a *App) onDomReady(ctx context.Context) {
	a.ctx = ctx
}

// onSecondInstanceLaunch is called when user clicks the dock icon while app is already running.
// We show the window so the user can interact with it again.
func (a *App) onSecondInstanceLaunch(secondInstanceData options.SecondInstanceData) {
	a.ShowWindow()
}

// beforeClose hides the window by default so the proxy can keep running.
// QuitApp sets quitting=true before allowing the process to exit.
func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return true
	}

	now := time.Now()
	if !a.quitHint.IsZero() && now.Sub(a.quitHint) <= 2*time.Second {
		a.mu.Unlock()
		go a.forceQuit()
		return true
	}
	a.quitHint = now
	a.mu.Unlock()

	message := "再按一次退出快捷键将停止代理并退出应用"
	a.emitLog("warn", message)
	runtime.EventsEmit(a.ctx, "quit:confirm", message)
	return true
}

// ShowWindow shows the main window
func (a *App) ShowWindow() {
	runtime.Show(a.ctx)
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

// HideWindow hides the main window
func (a *App) HideWindow() {
	runtime.Hide(a.ctx)
}

// MinimizeWindow minimises the main window.
func (a *App) MinimizeWindow() {
	runtime.WindowMinimise(a.ctx)
}

func (a *App) beginQuit() {
	go a.forceQuit()
}

// QuitApp fully quits the application
func (a *App) QuitApp() {
	a.beginQuit()
}

// RequestQuitShortcut requires two shortcut presses to avoid accidental exits.
func (a *App) RequestQuitShortcut() {
	now := time.Now()
	a.mu.Lock()
	shouldQuit := !a.quitHint.IsZero() && now.Sub(a.quitHint) <= 2*time.Second
	a.quitHint = now
	a.mu.Unlock()

	if shouldQuit {
		go a.forceQuit()
		return
	}

	message := "再按一次退出快捷键将停止代理并退出应用"
	a.emitLog("warn", message)
	runtime.EventsEmit(a.ctx, "quit:confirm", message)
}

func (a *App) forceQuit() {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return
	}
	a.quitting = true
	a.mu.Unlock()

	a.emitLog("info", "正在停止代理并退出应用")
	if err := a.StopProxy(); err != nil {
		runtime.LogWarningf(a.ctx, "stop proxy before exit failed: %v", err)
	}
	os.Exit(0)
}

func (a *App) emitLog(level string, message string) {
	entry := AppLog{
		Time:    time.Now().Format("15:04:05"),
		Level:   level,
		Message: message,
	}
	a.mu.Lock()
	a.logs = append(a.logs, entry)
	if len(a.logs) > 2000 {
		a.logs = a.logs[len(a.logs)-2000:]
	}
	a.saveAppStateLocked()
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "log", entry)
}

// GetStatus returns the current proxy status
func (a *App) GetStatus() ProxyStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	startedAt := ""
	if !a.startedAt.IsZero() {
		startedAt = a.startedAt.Format(time.RFC3339)
	}
	return ProxyStatus{
		Running:   a.running,
		Addr:      a.addr,
		Backend:   string(a.cfg.Backend),
		Models:    len(a.models),
		Model:     a.cfg.Model,
		StartedAt: startedAt,
	}
}

// GetConfig returns the current configuration.
// Timeout is returned in seconds for frontend convenience.
func (a *App) GetConfig() service.Config {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	cfg.Timeout = cfg.Timeout / time.Second
	return cfg
}

// GetDetectionInfo returns resolved IPC/remote details without exposing tokens.
func (a *App) GetDetectionInfo() DetectionInfo {
	a.mu.RLock()
	cfg := a.cfg
	addr := a.addr
	a.mu.RUnlock()

	if strings.TrimSpace(addr) == "" {
		addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	}
	baseURL := remote.ResolveBaseURLWithSource(cfg.RemoteBaseURL)
	info := DetectionInfo{
		ListenURL:           "http://" + addr,
		Backend:             string(cfg.Backend),
		BackendLabel:        backendLabel(cfg.Backend),
		RemoteBaseURL:       baseURL.URL,
		RemoteBaseURLSource: baseURL.Source,
	}

	if opts, err := lingmaipc.ResolveDialOptions(cfg.Transport, cfg.Pipe, cfg.WebSocketURL); err == nil {
		info.IPCSuccess = true
		info.IPCTransport = string(opts.Transport)
		switch opts.Transport {
		case lingmaipc.TransportPipe:
			info.IPCEndpoint = opts.PipePath
		case lingmaipc.TransportWebSocket:
			info.IPCEndpoint = opts.WebSocketURL
		}
	} else {
		info.IPCError = err.Error()
	}

	if cred, err := remote.LoadCredential(cfg.RemoteAuthFile); err == nil {
		info.RemoteCredentialSuccess = true
		info.RemoteCredentialSource = cred.Source
		info.RemoteUserID = maskIdentifier(cred.UserID)
		info.RemoteMachineID = maskIdentifier(cred.MachineID)
		info.RemoteTokenExpired = remote.IsExpired(cred, 0)
		if cred.TokenExpireTime > 0 {
			info.RemoteTokenExpireAt = time.UnixMilli(cred.TokenExpireTime).Format(time.RFC3339)
		}
	} else {
		info.RemoteCredentialError = err.Error()
	}

	return info
}

// UpdateConfig updates the configuration, saves to file, and restarts the proxy if running.
// Frontend sends Timeout in seconds; we convert to time.Duration.
func (a *App) UpdateConfig(cfg service.Config) error {
	// Convert seconds -> Duration if frontend sent a small value
	if cfg.Timeout > 0 && cfg.Timeout < time.Second {
		cfg.Timeout = cfg.Timeout * time.Second
	}

	a.mu.Lock()
	wasRunning := a.running
	a.cfg = cfg
	a.mu.Unlock()

	if err := a.saveConfig(cfg); err != nil {
		runtime.LogWarningf(a.ctx, "failed to save config: %v", err)
		a.emitLog("warn", fmt.Sprintf("Config updated but failed to save: %v", err))
	} else {
		a.emitLog("info", "Config saved to file")
	}

	if wasRunning {
		if err := a.StopProxy(); err != nil {
			return fmt.Errorf("stop failed: %w", err)
		}
		return a.StartProxy()
	}
	return nil
}

func (a *App) saveConfig(cfg service.Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "lingma-ipc-proxy")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	timeoutSec := int(cfg.Timeout.Seconds())
	fileCfg := map[string]any{
		"host":                    cfg.Host,
		"port":                    cfg.Port,
		"backend":                 string(cfg.Backend),
		"transport":               string(cfg.Transport),
		"pipe":                    cfg.Pipe,
		"websocket_url":           cfg.WebSocketURL,
		"remote_base_url":         cfg.RemoteBaseURL,
		"remote_auth_file":        cfg.RemoteAuthFile,
		"remote_version":          cfg.RemoteVersion,
		"cwd":                     cfg.Cwd,
		"current_file_path":       cfg.CurrentFilePath,
		"mode":                    cfg.Mode,
		"model":                   cfg.Model,
		"shell_type":              cfg.ShellType,
		"session_mode":            string(cfg.SessionMode),
		"timeout":                 timeoutSec,
		"remote_fallback_enabled": cfg.RemoteFallbackEnabled,
		"remote_fallback_models":  cfg.RemoteFallbackModels,
	}

	data, err := json.MarshalIndent(fileCfg, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(dir, "config.json")
	return os.WriteFile(path, data, 0644)
}

// StartProxy starts the lingma-ipc-proxy HTTP server
func (a *App) StartProxy() error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("proxy already running")
	}

	addr := fmt.Sprintf("%s:%d", a.cfg.Host, a.cfg.Port)
	cfg := a.cfg
	a.mu.Unlock()

	svc := service.New(cfg)

	warmupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := svc.Warmup(warmupCtx); err != nil {
		runtime.LogWarningf(a.ctx, "warmup failed: %v", err)
		a.emitLog("warn", fmt.Sprintf("Lingma IPC warmup failed: %v. %s", err, transportFallbackHint()))
	} else {
		runtime.LogInfo(a.ctx, "Lingma IPC warmup completed")
		a.emitLog("info", "Lingma IPC warmup completed")
	}
	cancel()

	server := httpapi.NewServer(addr, svc)
	server.OnRequest = func(method, path string, statusCode int, duration time.Duration, reqBody, respBody string) {
		inputTokens, outputTokens := extractTokenUsage(respBody)
		model := extractRequestModel(reqBody)
		record := RequestRecord{
			Time:         time.Now().Format("15:04:05"),
			Method:       method,
			Path:         path,
			Model:        model,
			StatusCode:   statusCode,
			Duration:     duration.Round(time.Millisecond).String(),
			Size:         formatPayloadSize(len(reqBody) + len(respBody)),
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  inputTokens + outputTokens,
			ReqBody:      reqBody,
			RespBody:     respBody,
		}
		a.mu.Lock()
		a.requests = append(a.requests, record)
		if len(a.requests) > 2000 {
			a.requests = a.requests[len(a.requests)-2000:]
		}
		a.accumulateTokenStatsLocked(record)
		a.saveAppStateLocked()
		a.mu.Unlock()
		runtime.EventsEmit(a.ctx, "requests:updated", a.GetRequests())
		runtime.EventsEmit(a.ctx, "usage:updated", a.GetTokenStats())
	}

	// Check if the port is available before claiming we're running
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %s is already in use: %w", addr, err)
	}
	ln.Close()

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			runtime.LogErrorf(a.ctx, "server error: %v", err)
			a.emitLog("error", fmt.Sprintf("Server error: %v", err))
			a.mu.Lock()
			a.running = false
			a.addr = ""
			a.startedAt = time.Time{}
			a.mu.Unlock()
		}
	}()

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("proxy already running")
	}
	a.server = server
	a.addr = addr
	a.running = true
	a.startedAt = time.Now()
	a.mu.Unlock()

	msg := fmt.Sprintf("Proxy started on http://%s", addr)
	runtime.LogInfof(a.ctx, msg)
	a.emitLog("info", msg)

	// Fetch models in background
	go a.fetchModels(addr)

	return nil
}

func (a *App) GetLogs() []AppLog {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AppLog, len(a.logs))
	copy(out, a.logs)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (a *App) ClearLogs() {
	a.mu.Lock()
	a.logs = nil
	a.saveAppStateLocked()
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "logs:updated", a.GetLogs())
}

// StopProxy stops the proxy server
func (a *App) StopProxy() error {
	a.mu.Lock()
	if !a.running || a.server == nil {
		a.mu.Unlock()
		return nil
	}

	server := a.server
	a.server = nil
	a.running = false
	a.addr = ""
	a.startedAt = time.Time{}
	a.models = nil
	a.mu.Unlock()

	runtime.EventsEmit(a.ctx, "status:updated", a.GetStatus())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		a.emitLog("warn", fmt.Sprintf("Proxy stop forced after graceful shutdown timeout: %v", err))
		return err
	}

	runtime.LogInfo(a.ctx, "proxy stopped")
	a.emitLog("info", "Proxy stopped")
	return nil
}

// GetModels returns the cached model list
func (a *App) GetModels() []ModelInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.models
}

// GetRequests returns recent HTTP request records
func (a *App) GetRequests() []RequestRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]RequestRecord, len(a.requests))
	copy(out, a.requests)
	// reverse so newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ClearRequests clears the request history
func (a *App) ClearRequests() {
	a.mu.Lock()
	a.requests = nil
	a.saveAppStateLocked()
	a.mu.Unlock()
	a.emitLog("info", "Request history cleared")
}

func (a *App) GetTokenStats() TokenStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	stats := a.stats
	if stats.ByModel != nil {
		stats.ByModel = cloneIntMap(stats.ByModel)
	}
	return stats
}

// RefreshModels probes the running proxy for the latest model list.
func (a *App) RefreshModels() ([]ModelInfo, error) {
	a.mu.RLock()
	running := a.running
	addr := a.addr
	a.mu.RUnlock()

	if !running || addr == "" {
		return nil, fmt.Errorf("proxy is not running")
	}

	models, err := a.fetchModels(addr)
	if err != nil {
		return nil, err
	}
	return models, nil
}

func (a *App) SelectModel(modelID string) (ProxyStatus, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return a.GetStatus(), fmt.Errorf("model id is required")
	}

	a.mu.Lock()
	found := len(a.models) == 0
	for _, model := range a.models {
		if model.ID == modelID {
			found = true
			break
		}
	}
	if !found {
		a.mu.Unlock()
		return a.GetStatus(), fmt.Errorf("model %q is not in the detected model list", modelID)
	}
	a.cfg.Model = modelID
	cfg := a.cfg
	server := a.server
	a.mu.Unlock()

	if server != nil {
		server.SetDefaultModel(modelID)
	}
	if err := a.saveConfig(cfg); err != nil {
		a.emitLog("warn", fmt.Sprintf("Model switched but config save failed: %v", err))
	}
	a.emitLog("info", fmt.Sprintf("已切换默认模型：%s", modelID))
	return a.GetStatus(), nil
}

func (a *App) fetchModels(addr string) ([]ModelInfo, error) {
	url := fmt.Sprintf("http://%s/v1/models", addr)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		runtime.LogWarningf(a.ctx, "fetch models failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		runtime.LogWarningf(a.ctx, "decode models failed: %v", err)
		return nil, err
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{ID: m.ID, Name: m.Name})
	}

	a.mu.Lock()
	a.models = models
	a.mu.Unlock()

	runtime.EventsEmit(a.ctx, "models:updated", models)
	if len(models) > 0 {
		a.emitLog("info", fmt.Sprintf("Loaded %d models", len(models)))
	}
	return models, nil
}

func extractRequestModel(reqBody string) string {
	if strings.TrimSpace(reqBody) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqBody), &payload); err != nil {
		return ""
	}
	if model, ok := payload["model"].(string); ok {
		return strings.TrimSpace(model)
	}
	return ""
}

func formatPayloadSize(bytes int) string {
	if bytes <= 0 {
		return "-"
	}
	const kb = 1024
	const mb = 1024 * kb
	if bytes >= mb {
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	}
	if bytes >= kb {
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	}
	return fmt.Sprintf("%d B", bytes)
}

type appStateFile struct {
	Requests []RequestRecord `json:"requests"`
	Logs     []AppLog        `json:"logs"`
	Stats    TokenStats      `json:"stats"`
}

func (a *App) loadAppState() error {
	path, err := appStatePath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state appStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = state.Requests
	a.logs = state.Logs
	a.stats = state.Stats
	if a.stats.ByModel == nil {
		a.stats.ByModel = map[string]int{}
	}
	a.reconcileTokenStatsLocked()
	return nil
}

func (a *App) saveAppStateLocked() {
	path, err := appStatePath()
	if err != nil {
		runtime.LogWarningf(a.ctx, "resolve app state path failed: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		runtime.LogWarningf(a.ctx, "create app state dir failed: %v", err)
		return
	}
	state := appStateFile{
		Requests: a.requests,
		Logs:     a.logs,
		Stats:    a.stats,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		runtime.LogWarningf(a.ctx, "marshal app state failed: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		runtime.LogWarningf(a.ctx, "write app state failed: %v", err)
	}
}

func appStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "lingma-ipc-proxy", "app-state.json"), nil
}

func (a *App) accumulateTokenStatsLocked(record RequestRecord) {
	a.stats.TotalRequests++
	if record.StatusCode >= 200 && record.StatusCode < 300 {
		a.stats.SuccessRequests++
	}
	a.stats.InputTokens += record.InputTokens
	a.stats.OutputTokens += record.OutputTokens
	a.stats.TotalTokens += record.TotalTokens
	if a.stats.ByModel == nil {
		a.stats.ByModel = map[string]int{}
	}
	model := strings.TrimSpace(record.Model)
	if model == "" {
		model = "-"
	}
	if record.TotalTokens > 0 {
		a.stats.ByModel[model] += record.TotalTokens
		if isUsageBearingRequest(record.Path) && model != "-" {
			a.stats.LastModel = model
		}
	}
	a.stats.LastUpdated = time.Now().Format(time.RFC3339)
}

func (a *App) reconcileTokenStatsLocked() {
	if a.stats.ByModel == nil {
		a.stats.ByModel = map[string]int{}
	}
	a.stats.LastModel = ""
	for i := len(a.requests) - 1; i >= 0; i-- {
		record := a.requests[i]
		model := strings.TrimSpace(record.Model)
		if model == "" || record.TotalTokens <= 0 || !isUsageBearingRequest(record.Path) {
			continue
		}
		a.stats.LastModel = model
		break
	}
}

func isUsageBearingRequest(path string) bool {
	switch strings.TrimSpace(path) {
	case "/v1/messages", "/v1/chat/completions", "/v1/completions":
		return true
	default:
		return false
	}
}

func cloneIntMap(src map[string]int) map[string]int {
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func extractTokenUsage(respBody string) (int, int) {
	if strings.TrimSpace(respBody) == "" {
		return 0, 0
	}
	input, output := extractUsageFromJSON(respBody)
	if input != 0 || output != 0 {
		return input, output
	}
	for _, line := range strings.Split(respBody, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		in, out := extractUsageFromJSON(payload)
		if in > 0 {
			input = in
		}
		if out > 0 {
			output = out
		}
	}
	return input, output
}

func extractUsageFromJSON(raw string) (int, int) {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return 0, 0
	}
	usage, ok := findUsageMap(payload)
	if !ok {
		return 0, 0
	}
	input := intFromAny(usage["input_tokens"]) + intFromAny(usage["prompt_tokens"])
	output := intFromAny(usage["output_tokens"]) + intFromAny(usage["completion_tokens"])
	return input, output
}

func findUsageMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if usage, ok := typed["usage"].(map[string]any); ok {
			return usage, true
		}
		for _, child := range typed {
			if usage, ok := findUsageMap(child); ok {
				return usage, true
			}
		}
	case []any:
		for _, child := range typed {
			if usage, ok := findUsageMap(child); ok {
				return usage, true
			}
		}
	}
	return nil, false
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}

func defaultConfig() service.Config {
	cfg := service.Config{
		Host:                  "127.0.0.1",
		Port:                  8095,
		Backend:               service.BackendRemote,
		Transport:             lingmaipc.TransportAuto,
		Cwd:                   defaultCwd(),
		Mode:                  "agent",
		Model:                 "kmodel",
		ShellType:             defaultShellType(),
		SessionMode:           service.SessionModeAuto,
		Timeout:               300 * time.Second,
		RemoteFallbackEnabled: true,
		RemoteFallbackModels:  service.DefaultRemoteFallbackModels(),
	}

	// Try to load config file from multiple locations
	configPaths := configSearchPaths()
	for _, configPath := range configPaths {
		if info, err := os.Stat(configPath); err == nil && !info.IsDir() {
			if data, err := os.ReadFile(configPath); err == nil {
				var fileCfg struct {
					Host                  string   `json:"host"`
					Port                  int      `json:"port"`
					Backend               string   `json:"backend"`
					Transport             string   `json:"transport"`
					Pipe                  string   `json:"pipe"`
					WebSocketURL          string   `json:"websocket_url"`
					RemoteBaseURL         string   `json:"remote_base_url"`
					RemoteAuthFile        string   `json:"remote_auth_file"`
					RemoteVersion         string   `json:"remote_version"`
					Cwd                   string   `json:"cwd"`
					CurrentFilePath       string   `json:"current_file_path"`
					Mode                  string   `json:"mode"`
					Model                 string   `json:"model"`
					ShellType             string   `json:"shell_type"`
					SessionMode           string   `json:"session_mode"`
					TimeoutSeconds        int      `json:"timeout"`
					RemoteFallbackEnabled *bool    `json:"remote_fallback_enabled"`
					RemoteFallbackModels  []string `json:"remote_fallback_models"`
				}
				if err := json.Unmarshal(data, &fileCfg); err == nil {
					if fileCfg.Host != "" {
						cfg.Host = fileCfg.Host
					}
					if fileCfg.Port > 0 {
						cfg.Port = fileCfg.Port
					}
					if fileCfg.Backend != "" {
						cfg.Backend = service.BackendMode(fileCfg.Backend)
					}
					if fileCfg.Transport != "" {
						if t, err := lingmaipc.ParseTransport(fileCfg.Transport); err == nil {
							cfg.Transport = t
						}
					}
					if fileCfg.Pipe != "" {
						cfg.Pipe = fileCfg.Pipe
					}
					if fileCfg.WebSocketURL != "" {
						cfg.WebSocketURL = fileCfg.WebSocketURL
					}
					if fileCfg.RemoteBaseURL != "" {
						cfg.RemoteBaseURL = fileCfg.RemoteBaseURL
					}
					if fileCfg.RemoteAuthFile != "" {
						cfg.RemoteAuthFile = fileCfg.RemoteAuthFile
					}
					if fileCfg.RemoteVersion != "" {
						cfg.RemoteVersion = fileCfg.RemoteVersion
					}
					if fileCfg.Cwd != "" {
						cfg.Cwd = fileCfg.Cwd
					}
					if fileCfg.CurrentFilePath != "" {
						cfg.CurrentFilePath = fileCfg.CurrentFilePath
					}
					if fileCfg.Mode != "" {
						cfg.Mode = fileCfg.Mode
					}
					if fileCfg.Model != "" {
						cfg.Model = fileCfg.Model
					}
					if fileCfg.ShellType != "" {
						cfg.ShellType = fileCfg.ShellType
					}
					if fileCfg.SessionMode != "" {
						cfg.SessionMode = service.SessionMode(fileCfg.SessionMode)
					}
					if fileCfg.TimeoutSeconds > 0 {
						cfg.Timeout = time.Duration(fileCfg.TimeoutSeconds) * time.Second
					}
					if fileCfg.RemoteFallbackEnabled != nil {
						cfg.RemoteFallbackEnabled = *fileCfg.RemoteFallbackEnabled
					}
					if len(fileCfg.RemoteFallbackModels) > 0 {
						cfg.RemoteFallbackModels = cleanConfigStrings(fileCfg.RemoteFallbackModels)
					}
				}
				break // loaded successfully
			}
		}
	}

	return cfg
}

func backendLabel(backend service.BackendMode) string {
	switch backend {
	case service.BackendRemote:
		return "远端 API"
	default:
		return "IPC 插件"
	}
}

func maskIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return string(runes[:1]) + "***"
	}
	return string(runes[:4]) + "..." + string(runes[len(runes)-4:])
}

func cleanConfigStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func configSearchPaths() []string {
	var paths []string
	// 1. Executable directory (for dev / portable mode)
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "lingma-ipc-proxy.json"))
	}
	// 2. Current working directory
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, "lingma-ipc-proxy.json"))
	}
	// 3. User home directory
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, "lingma-ipc-proxy.json"))
		paths = append(paths, filepath.Join(home, ".config", "lingma-ipc-proxy", "config.json"))
	}
	return paths
}

func defaultCwd() string {
	// Use the user's home directory as the default working directory
	// so it works out-of-the-box regardless of where the app is launched.
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func defaultShellType() string {
	if goruntime.GOOS == "windows" {
		return "powershell"
	}
	return "zsh"
}

func transportFallbackHint() string {
	return "请确认 Lingma 插件已启动并登录；如果自动探测失败，请到设置页手动填写：macOS WebSocket 示例 ws://127.0.0.1:36510/，Windows Named Pipe 示例 \\\\.\\pipe\\lingma-xxxx，或 Windows WebSocket 示例 ws://127.0.0.1:36510/。"
}
