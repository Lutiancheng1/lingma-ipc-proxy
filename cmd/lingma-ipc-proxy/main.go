package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lingma-ipc-proxy/internal/httpapi"
	"lingma-ipc-proxy/internal/lingmaipc"
	"lingma-ipc-proxy/internal/service"
)

type fileConfig struct {
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

func main() {
	cfg, configPath := loadConfig()
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	svc := service.New(cfg)
	warmupCtx, warmupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := svc.Warmup(warmupCtx); err != nil {
		log.Printf("warmup failed: %v", err)
	} else {
		log.Printf("Lingma IPC warmup completed")
	}
	warmupCancel()

	server := httpapi.NewServer(addr, svc)

	log.Printf("lingma-ipc-proxy listening on http://%s", addr)
	log.Printf("session mode: %s", cfg.SessionMode)
	log.Printf("transport: %s", cfg.Transport)
	log.Printf("mode: %s", cfg.Mode)
	if configPath != "" {
		log.Printf("config file: %s", configPath)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Fatal(err)
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() (service.Config, string) {
	cfg := service.Config{
		Host:                  "127.0.0.1",
		Port:                  8095,
		Backend:               service.BackendRemote,
		Transport:             lingmaipc.TransportAuto,
		Cwd:                   currentDir(),
		Mode:                  "agent",
		Model:                 "kmodel",
		ShellType:             defaultShellType(),
		SessionMode:           service.SessionModeAuto,
		Timeout:               300 * time.Second,
		RemoteFallbackEnabled: true,
		RemoteFallbackModels:  service.DefaultRemoteFallbackModels(),
	}

	configPath, configLoaded := resolveConfigPath()
	if configLoaded {
		fileCfg, err := readFileConfig(configPath)
		if err != nil {
			log.Fatalf("load config file %q: %v", configPath, err)
		}
		overlayFileConfig(&cfg, fileCfg)
	}

	overlayEnvConfig(&cfg)

	host := flag.String("host", cfg.Host, "Listen host")
	port := flag.Int("port", cfg.Port, "Listen port")
	transport := flag.String("transport", string(cfg.Transport), "Lingma transport: auto, pipe, websocket")
	backend := flag.String("backend", string(cfg.Backend), "Backend mode: ipc or remote")
	pipe := flag.String("pipe", cfg.Pipe, "Explicit Lingma named pipe path")
	wsURL := flag.String("ws-url", cfg.WebSocketURL, "Explicit Lingma local websocket URL")
	remoteBaseURL := flag.String("remote-base-url", cfg.RemoteBaseURL, "Remote Lingma API base URL")
	remoteAuthFile := flag.String("remote-auth-file", cfg.RemoteAuthFile, "Remote Lingma credentials.json path; empty reads ~/.lingma cache")
	remoteVersion := flag.String("remote-version", cfg.RemoteVersion, "Remote Lingma cosy version")
	cwd := flag.String("cwd", cfg.Cwd, "Working directory used when creating Lingma sessions")
	currentFilePath := flag.String("current-file-path", cfg.CurrentFilePath, "Current file path sent through ACP meta")
	mode := flag.String("mode", cfg.Mode, "Lingma ACP mode value")
	model := flag.String("model", cfg.Model, "Default Lingma model when API request omits model")
	shellType := flag.String("shell-type", cfg.ShellType, "Shell type sent through ACP meta")
	timeoutSeconds := flag.Int("timeout", int(cfg.Timeout/time.Second), "Per-request timeout in seconds")
	remoteFallbackEnabled := flag.Bool("remote-fallback", cfg.RemoteFallbackEnabled, "Enable remote timeout/5xx fallback to the next available model")
	remoteFallbackModels := flag.String("remote-fallback-models", strings.Join(cfg.RemoteFallbackModels, ","), "Comma-separated remote fallback model IDs")
	sessionMode := flag.String("session-mode", string(cfg.SessionMode), "Session mode: auto, fresh, reuse")
	config := flag.String("config", valueOr(configPath, filepath.Join(currentDir(), "lingma-ipc-proxy.json")), "Path to JSON config file")
	flag.Parse()

	parsedSessionMode := parseSessionMode(*sessionMode)
	parsedTransport := parseTransport(*transport)
	finalConfigPath := strings.TrimSpace(*config)

	cfg.Host = strings.TrimSpace(*host)
	cfg.Port = *port
	cfg.Backend = parseBackend(*backend)
	cfg.Transport = parsedTransport
	cfg.Pipe = strings.TrimSpace(*pipe)
	cfg.WebSocketURL = strings.TrimSpace(*wsURL)
	cfg.RemoteBaseURL = strings.TrimSpace(*remoteBaseURL)
	cfg.RemoteAuthFile = strings.TrimSpace(*remoteAuthFile)
	cfg.RemoteVersion = strings.TrimSpace(*remoteVersion)
	cfg.Cwd = strings.TrimSpace(*cwd)
	cfg.CurrentFilePath = strings.TrimSpace(*currentFilePath)
	cfg.Mode = strings.TrimSpace(*mode)
	cfg.Model = strings.TrimSpace(*model)
	cfg.ShellType = strings.TrimSpace(*shellType)
	cfg.SessionMode = parsedSessionMode
	cfg.Timeout = time.Duration(*timeoutSeconds) * time.Second
	cfg.RemoteFallbackEnabled = *remoteFallbackEnabled
	cfg.RemoteFallbackModels = splitCSV(*remoteFallbackModels)

	if configLoaded {
		configPath = finalConfigPath
	} else {
		configPath = ""
	}

	return cfg, configPath
}

func resolveConfigPath() (string, bool) {
	if path := strings.TrimSpace(lookupArgValue("--config")); path != "" {
		return path, true
	}
	if path := strings.TrimSpace(os.Getenv("LINGMA_PROXY_CONFIG")); path != "" {
		return path, true
	}
	defaultPath := filepath.Join(currentDir(), "lingma-ipc-proxy.json")
	if info, err := os.Stat(defaultPath); err == nil && !info.IsDir() {
		return defaultPath, true
	}
	return defaultPath, false
}

func readFileConfig(path string) (fileConfig, error) {
	var cfg fileConfig
	body, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func overlayFileConfig(dst *service.Config, src fileConfig) {
	if strings.TrimSpace(src.Host) != "" {
		dst.Host = strings.TrimSpace(src.Host)
	}
	if src.Port > 0 {
		dst.Port = src.Port
	}
	if strings.TrimSpace(src.Transport) != "" {
		dst.Transport = parseTransport(src.Transport)
	}
	if strings.TrimSpace(src.Backend) != "" {
		dst.Backend = parseBackend(src.Backend)
	}
	if strings.TrimSpace(src.Pipe) != "" {
		dst.Pipe = strings.TrimSpace(src.Pipe)
	}
	if strings.TrimSpace(src.WebSocketURL) != "" {
		dst.WebSocketURL = strings.TrimSpace(src.WebSocketURL)
	}
	if strings.TrimSpace(src.RemoteBaseURL) != "" {
		dst.RemoteBaseURL = strings.TrimSpace(src.RemoteBaseURL)
	}
	if strings.TrimSpace(src.RemoteAuthFile) != "" {
		dst.RemoteAuthFile = strings.TrimSpace(src.RemoteAuthFile)
	}
	if strings.TrimSpace(src.RemoteVersion) != "" {
		dst.RemoteVersion = strings.TrimSpace(src.RemoteVersion)
	}
	if strings.TrimSpace(src.Cwd) != "" {
		dst.Cwd = strings.TrimSpace(src.Cwd)
	}
	if strings.TrimSpace(src.CurrentFilePath) != "" {
		dst.CurrentFilePath = strings.TrimSpace(src.CurrentFilePath)
	}
	if strings.TrimSpace(src.Mode) != "" {
		dst.Mode = strings.TrimSpace(src.Mode)
	}
	if strings.TrimSpace(src.Model) != "" {
		dst.Model = strings.TrimSpace(src.Model)
	}
	if strings.TrimSpace(src.ShellType) != "" {
		dst.ShellType = strings.TrimSpace(src.ShellType)
	}
	if strings.TrimSpace(src.SessionMode) != "" {
		dst.SessionMode = parseSessionMode(src.SessionMode)
	}
	if src.TimeoutSeconds > 0 {
		dst.Timeout = time.Duration(src.TimeoutSeconds) * time.Second
	}
	if src.RemoteFallbackEnabled != nil {
		dst.RemoteFallbackEnabled = *src.RemoteFallbackEnabled
	}
	if len(src.RemoteFallbackModels) > 0 {
		dst.RemoteFallbackModels = cleanStringSlice(src.RemoteFallbackModels)
	}
}

func overlayEnvConfig(dst *service.Config) {
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_HOST")); value != "" {
		dst.Host = value
	}
	if value := envInt("LINGMA_PROXY_PORT", 0); value > 0 {
		dst.Port = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_TRANSPORT")); value != "" {
		dst.Transport = parseTransport(value)
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_BACKEND")); value != "" {
		dst.Backend = parseBackend(value)
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_IPC_PIPE")); value != "" {
		dst.Pipe = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_WS_URL")); value != "" {
		dst.WebSocketURL = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_REMOTE_BASE_URL")); value != "" {
		dst.RemoteBaseURL = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_REMOTE_AUTH_FILE")); value != "" {
		dst.RemoteAuthFile = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_REMOTE_VERSION")); value != "" {
		dst.RemoteVersion = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_CWD")); value != "" {
		dst.Cwd = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_CURRENT_FILE_PATH")); value != "" {
		dst.CurrentFilePath = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_MODE")); value != "" {
		dst.Mode = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_MODEL")); value != "" {
		dst.Model = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_SHELL_TYPE")); value != "" {
		dst.ShellType = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_PROXY_SESSION_MODE")); value != "" {
		dst.SessionMode = parseSessionMode(value)
	}
	if value := envInt("LINGMA_PROXY_TIMEOUT_SECONDS", 0); value > 0 {
		dst.Timeout = time.Duration(value) * time.Second
	}
	if value, ok := envBool("LINGMA_REMOTE_FALLBACK_ENABLED"); ok {
		dst.RemoteFallbackEnabled = value
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_REMOTE_FALLBACK_MODELS")); value != "" {
		dst.RemoteFallbackModels = splitCSV(value)
	}
}

func parseSessionMode(value string) service.SessionMode {
	mode := service.SessionMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case service.SessionModeAuto, service.SessionModeFresh, service.SessionModeReuse:
		return mode
	default:
		log.Fatalf("invalid session mode %q; expected auto, fresh, or reuse", value)
		return service.SessionModeAuto
	}
}

func parseBackend(value string) service.BackendMode {
	mode := service.BackendMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case "":
		return service.BackendRemote
	case service.BackendIPC:
		return service.BackendIPC
	case service.BackendRemote:
		return service.BackendRemote
	default:
		log.Fatalf("invalid backend %q; expected ipc or remote", value)
		return service.BackendIPC
	}
}

func parseTransport(value string) lingmaipc.Transport {
	transport, err := lingmaipc.ParseTransport(value)
	if err != nil {
		log.Fatal(err)
	}
	return transport
}

func lookupArgValue(flagName string) string {
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == flagName {
			if i+1 < len(os.Args) {
				return os.Args[i+1]
			}
			return ""
		}
		prefix := flagName + "="
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func envInt(key string, fallback int) int {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string) (bool, bool) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch value {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func splitCSV(value string) []string {
	return cleanStringSlice(strings.Split(value, ","))
}

func cleanStringSlice(values []string) []string {
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

func currentDir() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func defaultShellType() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "zsh"
}
