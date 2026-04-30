package remote

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

func newUUID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16])
}

func newHexID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		seq := atomic.AddUint64(&hexCounter, 1)
		return fmt.Sprintf("fallback%x%x", time.Now().UnixNano(), seq)
	}
	return hex.EncodeToString(data[:])
}
