package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lingma-ipc-proxy/internal/httpapi"
	"lingma-ipc-proxy/internal/service"
)

func main() {
	cfg := loadConfig()
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	svc := service.New(cfg)
	server := httpapi.NewServer(addr, svc)

	log.Printf("lingma-ipc-proxy listening on http://%s", addr)
	log.Printf("session mode: %s", cfg.SessionMode)

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

func loadConfig() service.Config {
	hostDefault := envString("LINGMA_PROXY_HOST", "127.0.0.1")
	portDefault := envInt("LINGMA_PROXY_PORT", 8095)
	pipeDefault := envString("LINGMA_IPC_PIPE", "")
	cwdDefault := envString("LINGMA_PROXY_CWD", currentDir())
	currentFilePathDefault := envString("LINGMA_PROXY_CURRENT_FILE_PATH", "")
	modeDefault := envString("LINGMA_PROXY_MODE", "agent")
	shellTypeDefault := envString("LINGMA_PROXY_SHELL_TYPE", "powershell")
	timeoutDefault := envInt("LINGMA_PROXY_TIMEOUT_SECONDS", 120)
	sessionModeDefault := envString("LINGMA_PROXY_SESSION_MODE", string(service.SessionModeAuto))

	host := flag.String("host", hostDefault, "Listen host")
	port := flag.Int("port", portDefault, "Listen port")
	pipe := flag.String("pipe", pipeDefault, "Explicit Lingma named pipe path")
	cwd := flag.String("cwd", cwdDefault, "Working directory used when creating Lingma sessions")
	currentFilePath := flag.String("current-file-path", currentFilePathDefault, "Current file path sent through ACP meta")
	mode := flag.String("mode", modeDefault, "Lingma ACP mode value")
	shellType := flag.String("shell-type", shellTypeDefault, "Shell type sent through ACP meta")
	timeoutSeconds := flag.Int("timeout", timeoutDefault, "Per-request timeout in seconds")
	sessionMode := flag.String("session-mode", sessionModeDefault, "Session mode: auto, fresh, reuse")
	flag.Parse()

	parsedSessionMode := service.SessionMode(strings.ToLower(strings.TrimSpace(*sessionMode)))
	switch parsedSessionMode {
	case service.SessionModeAuto, service.SessionModeFresh, service.SessionModeReuse:
	default:
		log.Fatalf("invalid --session-mode %q; expected auto, fresh, or reuse", *sessionMode)
	}

	return service.Config{
		Host:            strings.TrimSpace(*host),
		Port:            *port,
		Pipe:            strings.TrimSpace(*pipe),
		Cwd:             strings.TrimSpace(*cwd),
		CurrentFilePath: strings.TrimSpace(*currentFilePath),
		Mode:            strings.TrimSpace(*mode),
		ShellType:       strings.TrimSpace(*shellType),
		SessionMode:     parsedSessionMode,
		Timeout:         time.Duration(*timeoutSeconds) * time.Second,
	}
}

func envString(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return fallback
}

func currentDir() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
