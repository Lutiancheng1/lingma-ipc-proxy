package lingmaipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
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
	transport  framedTransport
	kind       Transport
	pendingMu  sync.Mutex
	pending    map[int]chan responseEnvelope
	subsMu     sync.RWMutex
	subs       map[int]chan Notification
	nextID     atomic.Int64
	nextSubID  atomic.Int64
	closeOnce  sync.Once
	closed     chan struct{}
	closeErr   atomic.Value
	responseMu sync.Mutex
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

func Connect(ctx context.Context, opts DialOptions) (*Client, error) {
	transport, err := connectTransport(ctx, opts)
	if err != nil {
		return nil, err
	}

	client := &Client{
		transport: transport,
		kind:      opts.Transport,
		pending:   make(map[int]chan responseEnvelope),
		subs:      make(map[int]chan Notification),
		closed:    make(chan struct{}),
	}
	go client.readLoop()
	return client, nil
}

func (c *Client) Request(ctx context.Context, method string, params any, out any) error {
	payload, id, err := c.buildRequest(method, params)
	if err != nil {
		return err
	}

	responseCh := make(chan responseEnvelope, 1)
	c.pendingMu.Lock()
	c.pending[id] = responseCh
	c.pendingMu.Unlock()

	if err := c.transport.WriteFrame(payload); err != nil {
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

func (c *Client) Send(method string, params any) error {
	payload, _, err := c.buildRequest(method, params)
	if err != nil {
		return err
	}
	return c.transport.WriteFrame(payload)
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

func (c *Client) Address() string {
	if c.transport == nil {
		return ""
	}
	return c.transport.Address()
}

func (c *Client) Transport() Transport {
	return c.kind
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		if err := c.transport.Close(); err != nil {
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

func (c *Client) buildRequest(method string, params any) ([]byte, int, error) {
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
		return nil, 0, fmt.Errorf("marshal request %s: %w", method, err)
	}
	return body, id, nil
}

func (c *Client) readLoop() {
	defer c.Close()
	for {
		body, err := c.transport.ReadFrame()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.closeErr.Store(err)
			}
			return
		}

		var envelope responseEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			c.closeErr.Store(fmt.Errorf("decode IPC frame: %w", err))
			return
		}

		if envelope.Method != "" {
			c.broadcast(Notification{JSONRPC: envelope.JSONRPC, Method: envelope.Method, Params: envelope.Params})
			if envelope.ID != nil {
				_ = c.sendEmptyResponse(*envelope.ID)
			}
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

func (c *Client) sendEmptyResponse(id int) error {
	c.responseMu.Lock()
	defer c.responseMu.Unlock()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  nil,
	})
	if err != nil {
		return err
	}
	return c.transport.WriteFrame(body)
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
