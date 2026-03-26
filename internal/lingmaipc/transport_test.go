package lingmaipc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSharedClientInfoFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".info.json")
	content := `{"websocketPort":36510,"pid":14060,"ipcServerPath":"\\\\.\\pipe\\lingma-bf0f32","isDev":false}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write shared info json: %v", err)
	}

	info, err := resolveSharedClientInfoFromPaths([]string{path})
	if err != nil {
		t.Fatalf("resolve shared info json: %v", err)
	}
	if info.WebSocketPort != 36510 {
		t.Fatalf("unexpected websocket port: %d", info.WebSocketPort)
	}
	if info.PID != 14060 {
		t.Fatalf("unexpected pid: %d", info.PID)
	}
	if info.IPCServerPath != `\\.\pipe\lingma-bf0f32` {
		t.Fatalf("unexpected pipe path: %q", info.IPCServerPath)
	}
}

func TestResolveSharedClientInfoFromLegacyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".info")
	content := "36510\n14060\n\\\\.\\pipe\\lingma-bf0f32\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write shared info legacy: %v", err)
	}

	info, err := resolveSharedClientInfoFromPaths([]string{path})
	if err != nil {
		t.Fatalf("resolve shared info legacy: %v", err)
	}
	if info.WebSocketPort != 36510 {
		t.Fatalf("unexpected websocket port: %d", info.WebSocketPort)
	}
	if info.PID != 14060 {
		t.Fatalf("unexpected pid: %d", info.PID)
	}
	if info.IPCServerPath != `\\.\pipe\lingma-bf0f32` {
		t.Fatalf("unexpected pipe path: %q", info.IPCServerPath)
	}
}

func TestNormalizeWebSocketURLAddsRootPath(t *testing.T) {
	got, err := normalizeWebSocketURL("ws://127.0.0.1:36510")
	if err != nil {
		t.Fatalf("normalize websocket url: %v", err)
	}
	if got != "ws://127.0.0.1:36510/" {
		t.Fatalf("unexpected normalized websocket url: %q", got)
	}
}
