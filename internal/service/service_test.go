package service

import (
	"errors"
	"testing"
)

func TestIsRecoverableIPCError(t *testing.T) {
	cases := []error{
		errors.New("write websocket frame: write tcp 127.0.0.1:64954->127.0.0.1:36510: use of closed network connection"),
		errors.New("broken pipe"),
		errors.New("Lingma IPC notification stream closed"),
	}
	for _, err := range cases {
		if !isRecoverableIPCError(err) {
			t.Fatalf("expected recoverable error: %v", err)
		}
	}
}

func TestIsRecoverableIPCErrorIgnoresModelErrors(t *testing.T) {
	if isRecoverableIPCError(errors.New("timed out while waiting for Lingma IPC to finish responding")) {
		t.Fatal("timeout should not be treated as an immediate reconnect retry")
	}
}
