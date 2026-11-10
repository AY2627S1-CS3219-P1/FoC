package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const realtimePath = "/communication.v1.RealtimeService/WatchChanges"

// Probe the first framed response through the actual Gateway. A unary JSON
// response or buffered stream must not count as a healthy streaming endpoint.
func (a *authProbe) verifyRealtime(token string) error {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	body := []byte{0, 0, 0, 0, 2, '{', '}'}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+realtimePath, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid live update probe")
	}
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", a.origin)
	response, err := a.client.Do(req)
	if err != nil {
		return errors.New("live update stream unavailable through gateway")
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "application/connect+json") {
		return errors.New("invalid live update stream response")
	}
	header := make([]byte, 5)
	if _, err := io.ReadFull(response.Body, header); err != nil {
		return errors.New("live update ready frame was not flushed")
	}
	size := binary.BigEndian.Uint32(header[1:])
	if header[0] != 0 || size > 1<<20 {
		return errors.New("invalid live update ready envelope")
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(response.Body, payload); err != nil {
		return errors.New("incomplete live update ready frame")
	}
	var message struct {
		Ready bool `json:"ready"`
	}
	if json.Unmarshal(payload, &message) != nil || !message.Ready {
		return errors.New("live update stream was not ready")
	}
	return nil
}
