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
	"lingma-ipc-proxy/internal/service"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
// RequestRecord stores a single HTTP request summary
type RequestRecord struct {
	Time       string `json:"time"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	StatusCode int    `json:"statusCode"`
	Duration   string `json:"duration"`
	ReqBody    string `json:"reqBody,omitempty"`
	RespBody   string `json:"respBody,omitempty"`
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
	Models    int    `json:"models"`
	Model     string `json:"model,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.cfg = defaultConfig()

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
	runtime.EventsEmit(a.ctx, "log", map[string]string{
		"level":   level,
		"message": message,
	})
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
		"host":              cfg.Host,
		"port":              cfg.Port,
		"backend":           string(cfg.Backend),
		"transport":         string(cfg.Transport),
		"pipe":              cfg.Pipe,
		"websocket_url":     cfg.WebSocketURL,
		"remote_base_url":   cfg.RemoteBaseURL,
		"remote_auth_file":  cfg.RemoteAuthFile,
		"remote_version":    cfg.RemoteVersion,
		"cwd":               cfg.Cwd,
		"current_file_path": cfg.CurrentFilePath,
		"mode":              cfg.Mode,
		"model":             cfg.Model,
		"shell_type":        cfg.ShellType,
		"session_mode":      string(cfg.SessionMode),
		"timeout":           timeoutSec,
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
	defer a.mu.Unlock()

	if a.running {
		return fmt.Errorf("proxy already running")
	}

	addr := fmt.Sprintf("%s:%d", a.cfg.Host, a.cfg.Port)
	svc := service.New(a.cfg)

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
		a.mu.Lock()
		a.requests = append(a.requests, RequestRecord{
			Time:       time.Now().Format("15:04:05"),
			Method:     method,
			Path:       path,
			StatusCode: statusCode,
			Duration:   duration.Round(time.Millisecond).String(),
			ReqBody:    reqBody,
			RespBody:   respBody,
		})
		if len(a.requests) > 100 {
			a.requests = a.requests[len(a.requests)-100:]
		}
		a.mu.Unlock()
		runtime.EventsEmit(a.ctx, "requests:updated", a.GetRequests())
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

	a.server = server
	a.addr = addr
	a.running = true
	a.startedAt = time.Now()

	msg := fmt.Sprintf("Proxy started on http://%s", addr)
	runtime.LogInfof(a.ctx, msg)
	a.emitLog("info", msg)

	// Fetch models in background
	go a.fetchModels(addr)

	return nil
}

// ClearLogs is a no-op backend helper (logs are kept in frontend memory)
func (a *App) ClearLogs() {}

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
	a.mu.Unlock()
	a.emitLog("info", "Request history cleared")
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

func defaultConfig() service.Config {
	cfg := service.Config{
		Host:        "127.0.0.1",
		Port:        8095,
		Backend:     service.BackendIPC,
		Transport:   lingmaipc.TransportAuto,
		Cwd:         defaultCwd(),
		Mode:        "agent",
		Model:       "MiniMax-M2.7",
		ShellType:   defaultShellType(),
		SessionMode: service.SessionModeAuto,
		Timeout:     120 * time.Second,
	}

	// Try to load config file from multiple locations
	configPaths := configSearchPaths()
	for _, configPath := range configPaths {
		if info, err := os.Stat(configPath); err == nil && !info.IsDir() {
			if data, err := os.ReadFile(configPath); err == nil {
				var fileCfg struct {
					Host            string `json:"host"`
					Port            int    `json:"port"`
					Backend         string `json:"backend"`
					Transport       string `json:"transport"`
					Pipe            string `json:"pipe"`
					WebSocketURL    string `json:"websocket_url"`
					RemoteBaseURL   string `json:"remote_base_url"`
					RemoteAuthFile  string `json:"remote_auth_file"`
					RemoteVersion   string `json:"remote_version"`
					Cwd             string `json:"cwd"`
					CurrentFilePath string `json:"current_file_path"`
					Mode            string `json:"mode"`
					Model           string `json:"model"`
					ShellType       string `json:"shell_type"`
					SessionMode     string `json:"session_mode"`
					TimeoutSeconds  int    `json:"timeout"`
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
				}
				break // loaded successfully
			}
		}
	}

	return cfg
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
