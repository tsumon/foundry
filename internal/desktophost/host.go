// Package desktophost implements the private parent/controller lifetime protocol.
// Capabilities never enter arguments, environment, readiness, or renderer state.
package desktophost

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
)

type Session struct {
	Context    context.Context
	Capability string
}

func Open(parent context.Context, input io.Reader) (*Session, error) {
	reader := bufio.NewReaderSize(input, 128)
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) != 65 {
		return nil, errors.New("invalid desktop handshake")
	}
	token := string(line[:64])
	if _, err = hex.DecodeString(token); err != nil {
		return nil, errors.New("invalid desktop handshake")
	}
	ctx, cancel := context.WithCancel(parent)
	go func() { _, _ = io.Copy(io.Discard, reader); cancel() }()
	return &Session{Context: ctx, Capability: token}, nil
}

func Protect(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Foundry-Desktop")), []byte(token)) != 1 {
			http.Error(w, "desktop capability required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func Ready(output io.Writer, url, dataDir string) error {
	return json.NewEncoder(output).Encode(struct {
		Protocol int    `json:"protocol"`
		URL      string `json:"url"`
		PID      int    `json:"pid"`
		DataDir  string `json:"dataDir"`
	}{1, url, os.Getpid(), dataDir})
}
