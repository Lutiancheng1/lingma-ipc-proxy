//go:build windows

package lingmaipc

import (
	"context"
	"fmt"
	"net"
	"sync"

	winio "github.com/Microsoft/go-winio"
)

type pipeTransport struct {
	path   string
	conn   net.Conn
	reader *framedReader
	write  sync.Mutex
}

func connectPipeTransport(ctx context.Context, pipePath string) (framedTransport, error) {
	conn, err := winio.DialPipeContext(ctx, pipePath)
	if err != nil {
		return nil, fmt.Errorf("connect Lingma IPC pipe %s: %w", pipePath, err)
	}
	return &pipeTransport{
		path:   pipePath,
		conn:   conn,
		reader: newFramedReader(conn),
	}, nil
}

func (t *pipeTransport) ReadFrame() ([]byte, error) {
	return t.reader.ReadFrame()
}

func (t *pipeTransport) WriteFrame(body []byte) error {
	t.write.Lock()
	defer t.write.Unlock()

	frame := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body)))
	if _, err := t.conn.Write(frame); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if _, err := t.conn.Write(body); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

func (t *pipeTransport) Close() error {
	return t.conn.Close()
}

func (t *pipeTransport) Address() string {
	return t.path
}
