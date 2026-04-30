package lingmaipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Transport string

const (
	TransportAuto      Transport = "auto"
	TransportPipe      Transport = "pipe"
	TransportWebSocket Transport = "websocket"
)

type DialOptions struct {
	Transport    Transport
	PipePath     string
	WebSocketURL string
}

type sharedClientInfo struct {
	WebSocketPort int    `json:"websocketPort"`
	PID           int    `json:"pid"`
	IPCServerPath string `json:"ipcServerPath"`
}

type framedTransport interface {
	ReadFrame() ([]byte, error)
	WriteFrame([]byte) error
	Close() error
	Address() string
}

func ParseTransport(value string) (Transport, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(TransportAuto):
		return TransportAuto, nil
	case string(TransportPipe):
		return TransportPipe, nil
	case "ws", string(TransportWebSocket):
		return TransportWebSocket, nil
	default:
		return "", fmt.Errorf("invalid Lingma transport %q; expected auto, pipe, or websocket", value)
	}
}

func ResolveDialOptions(transport Transport, explicitPipe string, explicitWebSocketURL string) (DialOptions, error) {
	switch transport {
	case "", TransportAuto:
		if hasConfiguredWebSocketURL(explicitWebSocketURL) {
			wsURL, err := ResolveWebSocketURL(explicitWebSocketURL)
			if err != nil {
				return DialOptions{}, err
			}
			return DialOptions{Transport: TransportWebSocket, WebSocketURL: wsURL}, nil
		}

		if runtime.GOOS == "windows" {
			pipePath, pipeErr := ResolvePipePath(explicitPipe)
			if pipeErr == nil {
				return DialOptions{Transport: TransportPipe, PipePath: pipePath}, nil
			}
			wsURL, wsErr := ResolveWebSocketURL(explicitWebSocketURL)
			if wsErr == nil {
				return DialOptions{Transport: TransportWebSocket, WebSocketURL: wsURL}, nil
			}
			return DialOptions{}, fmt.Errorf("resolve Lingma transport automatically: pipe: %w; websocket: %v", pipeErr, wsErr)
		}

		wsURL, wsErr := ResolveWebSocketURL(explicitWebSocketURL)
		if wsErr == nil {
			return DialOptions{Transport: TransportWebSocket, WebSocketURL: wsURL}, nil
		}
		return DialOptions{}, fmt.Errorf("resolve Lingma transport automatically on %s: websocket: %w", runtime.GOOS, wsErr)
	case TransportPipe:
		pipePath, err := ResolvePipePath(explicitPipe)
		if err != nil {
			return DialOptions{}, err
		}
		return DialOptions{Transport: TransportPipe, PipePath: pipePath}, nil
	case TransportWebSocket:
		wsURL, err := ResolveWebSocketURL(explicitWebSocketURL)
		if err != nil {
			return DialOptions{}, err
		}
		return DialOptions{Transport: TransportWebSocket, WebSocketURL: wsURL}, nil
	default:
		return DialOptions{}, fmt.Errorf("unsupported Lingma transport %q", transport)
	}
}

func ResolvePipePath(explicit string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("Lingma pipe transport currently requires Windows")
	}

	if pipe := strings.TrimSpace(explicit); pipe != "" {
		return normalizePipePath(pipe), nil
	}
	if pipe := strings.TrimSpace(os.Getenv("LINGMA_IPC_PIPE")); pipe != "" {
		return normalizePipePath(pipe), nil
	}
	if info, err := resolveSharedClientInfo(); err == nil {
		if pipe := strings.TrimSpace(info.IPCServerPath); pipe != "" {
			return normalizePipePath(pipe), nil
		}
	}

	entries, err := os.ReadDir(PipeDir)
	if err != nil {
		return "", fmt.Errorf("enumerate Lingma named pipes: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, PipePrefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", errors.New("no active Lingma named pipe was found")
	}
	return PipeDir + names[len(names)-1], nil
}

func ResolveWebSocketURL(explicit string) (string, error) {
	value := strings.TrimSpace(explicit)
	if value == "" {
		value = strings.TrimSpace(os.Getenv("LINGMA_PROXY_WS_URL"))
	}
	if value != "" {
		return normalizeWebSocketURL(value)
	}

	info, err := resolveSharedClientInfo()
	if err != nil {
		return "", fmt.Errorf("discover Lingma websocket URL: %w", err)
	}
	if info.WebSocketPort <= 0 {
		return "", errors.New("Lingma shared client info does not include a websocketPort")
	}
	return normalizeWebSocketURL(fmt.Sprintf("ws://127.0.0.1:%d/", info.WebSocketPort))
}

func hasConfiguredWebSocketURL(explicit string) bool {
	return strings.TrimSpace(explicit) != "" || strings.TrimSpace(os.Getenv("LINGMA_PROXY_WS_URL")) != ""
}

func normalizePipePath(pipe string) string {
	if strings.HasPrefix(pipe, PipeDir) {
		return pipe
	}
	return PipeDir + pipe
}

func normalizeWebSocketURL(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse Lingma websocket URL %q: %w", value, err)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return "", fmt.Errorf("Lingma websocket URL must start with ws:// or wss://: %q", value)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("Lingma websocket URL is missing a host: %q", value)
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

func resolveSharedClientInfo() (sharedClientInfo, error) {
	return resolveSharedClientInfoFromPaths(defaultSharedClientInfoPaths())
}

func defaultSharedClientInfoPaths() []string {
	if explicit := strings.TrimSpace(os.Getenv("LINGMA_SHARED_CLIENT_INFO")); explicit != "" {
		return []string{explicit}
	}

	bases := make([]string, 0, 8)
	if userConfigDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(userConfigDir) != "" {
		bases = append(bases, userConfigDir)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		bases = append(bases,
			filepath.Join(home, ".lingma", "vscode"),
			filepath.Join(home, ".lingma"),
		)
	}
	for _, envName := range []string{"APPDATA", "LOCALAPPDATA", "ProgramData"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			bases = append(bases, value)
		}
	}

	seen := make(map[string]struct{})
	paths := make([]string, 0, len(bases)*2)
	for _, base := range uniquePathStrings(bases) {
		cacheDirs := []string{
			filepath.Join(base, "Lingma", "SharedClientCache"),
			filepath.Join(base, "Lingma", "sharedClientCache"),
			filepath.Join(base, "SharedClientCache"),
			filepath.Join(base, "sharedClientCache"),
		}
		for _, cacheDir := range cacheDirs {
			for _, name := range []string{".info.json", ".info"} {
				path := filepath.Join(cacheDir, name)
				if _, ok := seen[path]; ok {
					continue
				}
				seen[path] = struct{}{}
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func uniquePathStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned := filepath.Clean(value)
		key := strings.ToLower(cleaned)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func resolveSharedClientInfoFromPaths(paths []string) (sharedClientInfo, error) {
	var parseErrors []string
	foundFile := false

	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			parseErrors = append(parseErrors, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		foundFile = true

		info, err := parseSharedClientInfo(body)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if info.WebSocketPort <= 0 && strings.TrimSpace(info.IPCServerPath) == "" {
			parseErrors = append(parseErrors, fmt.Sprintf("%s: no websocketPort or ipcServerPath present", path))
			continue
		}
		return info, nil
	}

	if !foundFile {
		return sharedClientInfo{}, errors.New("no Lingma shared client cache info file was found")
	}
	if len(parseErrors) == 0 {
		return sharedClientInfo{}, errors.New("Lingma shared client cache info was empty")
	}
	return sharedClientInfo{}, errors.New(strings.Join(parseErrors, "; "))
}

func parseSharedClientInfo(body []byte) (sharedClientInfo, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return sharedClientInfo{}, errors.New("file is empty")
	}
	if trimmed[0] == '{' {
		var info sharedClientInfo
		if err := json.Unmarshal(trimmed, &info); err != nil {
			return sharedClientInfo{}, fmt.Errorf("parse JSON shared client info: %w", err)
		}
		return info, nil
	}
	return parseLegacySharedClientInfo(string(trimmed))
}

func parseLegacySharedClientInfo(body string) (sharedClientInfo, error) {
	lines := make([]string, 0, 3)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return sharedClientInfo{}, errors.New("legacy shared client info is empty")
	}

	port, err := strconv.Atoi(lines[0])
	if err != nil {
		return sharedClientInfo{}, fmt.Errorf("parse legacy websocket port %q: %w", lines[0], err)
	}
	info := sharedClientInfo{WebSocketPort: port}

	if len(lines) >= 2 {
		pid, err := strconv.Atoi(lines[1])
		if err != nil {
			return sharedClientInfo{}, fmt.Errorf("parse legacy pid %q: %w", lines[1], err)
		}
		info.PID = pid
	}
	if len(lines) >= 3 {
		info.IPCServerPath = lines[2]
	}
	return info, nil
}

func connectTransport(ctx context.Context, opts DialOptions) (framedTransport, error) {
	switch opts.Transport {
	case TransportPipe:
		return connectPipeTransport(ctx, opts.PipePath)
	case TransportWebSocket:
		return connectWebSocketTransport(ctx, opts.WebSocketURL)
	default:
		return nil, fmt.Errorf("unsupported Lingma transport %q", opts.Transport)
	}
}

type websocketTransport struct {
	url     string
	conn    *websocket.Conn
	buffer  bytes.Buffer
	writeMu sync.Mutex
}

func connectWebSocketTransport(ctx context.Context, wsURL string) (*websocketTransport, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("connect Lingma websocket %s: %w", wsURL, err)
	}
	return &websocketTransport{url: wsURL, conn: conn}, nil
}

func (t *websocketTransport) ReadFrame() ([]byte, error) {
	for {
		if body, ok, err := tryReadBufferedFrame(&t.buffer); ok || err != nil {
			return body, err
		}

		messageType, payload, err := t.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		t.buffer.Write(payload)
	}
}

func (t *websocketTransport) WriteFrame(body []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	frame := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body)))
	frame = append(frame, body...)
	if err := t.conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		return fmt.Errorf("write websocket frame: %w", err)
	}
	return nil
}

func (t *websocketTransport) Close() error {
	return t.conn.Close()
}

func (t *websocketTransport) Address() string {
	return t.url
}

type framedReader struct {
	reader *bufio.Reader
}

func newFramedReader(r io.Reader) *framedReader {
	return &framedReader{reader: bufio.NewReader(r)}
}

func (r *framedReader) ReadFrame() ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == "\r\n" {
			break
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			raw := strings.TrimSpace(line[len("content-length:"):])
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("parse content length %q: %w", raw, err)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r.reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func tryReadBufferedFrame(buffer *bytes.Buffer) ([]byte, bool, error) {
	data := buffer.Bytes()
	headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		return nil, false, nil
	}

	contentLength := -1
	for _, rawLine := range bytes.Split(data[:headerEnd], []byte("\r\n")) {
		line := strings.TrimSpace(string(rawLine))
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			raw := strings.TrimSpace(line[len("content-length:"):])
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, false, fmt.Errorf("parse content length %q: %w", raw, err)
			}
			contentLength = n
			break
		}
	}
	if contentLength < 0 {
		return nil, false, errors.New("missing Content-Length header")
	}

	bodyStart := headerEnd + len("\r\n\r\n")
	if len(data[bodyStart:]) < contentLength {
		return nil, false, nil
	}

	frame := make([]byte, contentLength)
	copy(frame, data[bodyStart:bodyStart+contentLength])
	buffer.Next(bodyStart + contentLength)
	return frame, true, nil
}
