package model

import (
	"net/http"
	"testing"
	"time"
)

func TestCompletionTransportAllowsBoundedReasoning(t *testing.T) {
	router := NewRouter(nil, nil, RouterOptions{})
	transport := router.http.Transport.(*http.Transport)
	if router.http.Timeout != 5*time.Minute || transport.ResponseHeaderTimeout != 5*time.Minute {
		t.Fatalf("completion timeouts=%s/%s", router.http.Timeout, transport.ResponseHeaderTimeout)
	}
	if NewClient(nil).http.Timeout != 5*time.Minute {
		t.Fatal("legacy client still cuts off reasoning at 90 seconds")
	}
	if transport.Proxy != nil || !transport.DisableKeepAlives {
		t.Fatal("transport isolation or no-replay policy changed")
	}
}
