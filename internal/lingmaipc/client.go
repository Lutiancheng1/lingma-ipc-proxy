package lingmaipc

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	winio "github.com/Microsoft/go-winio"
)

const (
	PipeDir    = `\\.\pipe\`
	PipePrefix = "lingma-"

	MetaRequestID       = "ai-coding/request-id"
	MetaMode            = "ai-coding/mode"
	MetaModel           = "ai-coding/model"
	MetaShellType       = "ai-coding/shell-type"
	MetaCurrentFilePath = "ai-coding/current-file-path"
	MetaEnabledMCP      = "ai-coding/enabled-mcp-servers"
)

type MetaOptions struct {
	RequestID       string
	Mode            string
	Model           string
	ShellType       string
	CurrentFilePath string
	EnabledMCP      []any
}

type Notification struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type responseEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  map[string]any  `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type Client struct {
	conn      net.Conn
	reader    *bufio.Reader
	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[int]chan responseEnvelope
	subsMu    sync.RWMutex
	subs      map[int]chan Notification
	nextID    atomic.Int64
	nextSubID atomic.Int64
	closeOnce sync.Once
	closed    chan struct{}
	closeErr  atomic.Value
}

func ResolvePipePath(explicit string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("Lingma IPC proxy currently requires Windows")
	}

	if pipe := strings.TrimSpace(explicit); pipe != "" {
		return normalizePipePath(pipe), nil
	}
	if pipe := strings.TrimSpace(os.Getenv("LINGMA_IPC_PIPE")); pipe != "" {
		return normalizePipePath(pipe), nil
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

func normalizePipePath(pipe string) string {
	if strings.HasPrefix(pipe, PipeDir) {
		return pipe
	}
	return PipeDir + pipe
}

func DefaultShellType() string {
	if shellType := strings.TrimSpace(os.Getenv("LINGMA_PROXY_SHELL_TYPE")); shellType != "" {
		return shellType
	}
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		parts := strings.FieldsFunc(shell, func(r rune) bool { return r == '/' || r == '\\' })
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return "sh"
}

func CreateRequestID(prefix string) string {
	if prefix == "" {
		prefix = "ipc"
	}
	token := make([]byte, 4)
	if _, err := rand.Read(token); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(token))
}

func CreateMeta(opts MetaOptions) map[string]any {
	meta := map[string]any{
		MetaRequestID:  valueOr(opts.RequestID, CreateRequestID("ipc")),
		MetaShellType:  valueOr(opts.ShellType, DefaultShellType()),
		MetaEnabledMCP: emptySliceIfNil(opts.EnabledMCP),
	}
	if strings.TrimSpace(opts.Mode) != "" {
		meta[MetaMode] = strings.TrimSpace(opts.Mode)
	}
	if strings.TrimSpace(opts.Model) != "" {
		meta[MetaModel] = strings.TrimSpace(opts.Model)
	}
	if strings.TrimSpace(opts.CurrentFilePath) != "" {
		meta[MetaCurrentFilePath] = strings.TrimSpace(opts.CurrentFilePath)
	}
	return meta
}

func Connect(ctx context.Context, pipePath string) (*Client, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("Lingma IPC proxy currently requires Windows")
	}

	conn, err := winio.DialPipeContext(ctx, pipePath)
	if err != nil {
		return nil, fmt.Errorf("connect Lingma IPC pipe %s: %w", pipePath, err)
	}

	client := &Client{
		conn:    conn,
		reader:  bufio.NewReader(conn),
		pending: make(map[int]chan responseEnvelope),
		subs:    make(map[int]chan Notification),
		closed:  make(chan struct{}),
	}
	go client.readLoop()
	return client, nil
}

func (c *Client) Request(ctx context.Context, method string, params any, out any) error {
	if params == nil {
		params = map[string]any{}
	}

	id := int(c.nextID.Add(1))
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request %s: %w", method, err)
	}

	responseCh := make(chan responseEnvelope, 1)
	c.pendingMu.Lock()
	c.pending[id] = responseCh
	c.pendingMu.Unlock()

	if err := c.writeFrame(body); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return c.closeError()
	case resp := <-responseCh:
		if resp.Error != nil {
			return fmt.Errorf("Lingma IPC %s failed: %s", method, resp.Error.Message)
		}
		if out == nil || len(resp.Result) == 0 || string(resp.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
		return nil
	}
}

func (c *Client) Subscribe() (<-chan Notification, func()) {
	id := int(c.nextSubID.Add(1))
	ch := make(chan Notification, 2048)
	c.subsMu.Lock()
	c.subs[id] = ch
	c.subsMu.Unlock()

	cancel := func() {
		c.subsMu.Lock()
		if sub, ok := c.subs[id]; ok {
			delete(c.subs, id)
			close(sub)
		}
		c.subsMu.Unlock()
	}
	return ch, cancel
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		if err := c.conn.Close(); err != nil {
			c.closeErr.Store(err)
		}
		c.failPending(io.EOF)
		c.closeAllSubs()
	})
	if v := c.closeErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (c *Client) writeFrame(body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	frame := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body)))
	if _, err := c.conn.Write(frame); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if _, err := c.conn.Write(body); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

func (c *Client) readLoop() {
	defer c.Close()
	for {
		body, err := c.readFrame()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				c.closeErr.Store(err)
			}
			return
		}

		var envelope responseEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			c.closeErr.Store(fmt.Errorf("decode IPC frame: %w", err))
			return
		}

		if envelope.Method != "" && envelope.ID == nil {
			c.broadcast(Notification{JSONRPC: envelope.JSONRPC, Method: envelope.Method, Params: envelope.Params})
			continue
		}

		if envelope.ID == nil {
			continue
		}

		c.pendingMu.Lock()
		ch, ok := c.pending[*envelope.ID]
		if ok {
			delete(c.pending, *envelope.ID)
		}
		c.pendingMu.Unlock()
		if ok {
			ch <- envelope
			close(ch)
		}
	}
}

func (c *Client) readFrame() ([]byte, error) {
	contentLength := -1
	for {
		line, err := c.reader.ReadString('\n')
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
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *Client) broadcast(notification Notification) {
	c.subsMu.RLock()
	defer c.subsMu.RUnlock()
	for _, ch := range c.subs {
		ch <- notification
	}
}

func (c *Client) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- responseEnvelope{Error: &rpcError{Message: err.Error()}}
		close(ch)
	}
}

func (c *Client) closeAllSubs() {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for id, ch := range c.subs {
		delete(c.subs, id)
		close(ch)
	}
}

func (c *Client) closeError() error {
	if v := c.closeErr.Load(); v != nil {
		return v.(error)
	}
	return io.EOF
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func emptySliceIfNil(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}
