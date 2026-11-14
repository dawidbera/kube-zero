// Package proxy implements the Wake-on-Traffic reverse proxy for KubeZero.
package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var (
	// ErrPayloadTooLarge is returned when the request body exceeds the configured memory limit.
	ErrPayloadTooLarge = errors.New("request payload exceeds maximum allowed buffer size")
)

// BufferedRequestBody reads and buffers the HTTP request body up to maxBytes.
// If the body exceeds maxBytes, it returns ErrPayloadTooLarge.
// After reading, it restores r.Body with an io.NopCloser so the body can be re-read by the proxy.
func BufferedRequestBody(r *http.Request, maxBytes int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024 // Default 10MB
	}

	// Read up to maxBytes + 1 to detect overflow
	limitedReader := io.LimitReader(r.Body, maxBytes+1)
	buf, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	if int64(len(buf)) > maxBytes {
		return nil, ErrPayloadTooLarge
	}

	// Restore body for downstream forwarding
	r.Body = io.NopCloser(bytes.NewReader(buf))
	return buf, nil
}
