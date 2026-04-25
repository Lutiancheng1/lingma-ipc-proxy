//go:build !windows

package lingmaipc

import (
	"context"
	"errors"
)

func connectPipeTransport(ctx context.Context, pipePath string) (framedTransport, error) {
	return nil, errors.New("pipe transport is only supported on Windows")
}
