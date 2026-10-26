package model

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"foundry/internal/domain"
)

type exchangePhase struct {
	start time.Time
	end   time.Time
	count int
	done  int
	ok    bool
}

// A request-local observer owns no network, timers, files, body readers or
// credentials. Late/cancelled transport callbacks are ignored after freeze.
type exchangeObserver struct {
	mu     sync.Mutex
	frozen bool
	start  time.Time
	phases [3]exchangePhase
	d      domain.HTTPExchangeDiagnostic
}

// ObserveInferenceRequest attaches bounded observations only. It performs no
// dispatch, credential access, retry or body read. Call finish immediately after Do.
func ObserveInferenceRequest(req *http.Request, transport http.RoundTripper) (*http.Request, func(*http.Response) domain.HTTPExchangeDiagnostic) {
	o := newExchangeObserver(transport)
	req.Header.Set("X-Client-Request-Id", o.d.ClientRequestID)
	return req.WithContext(httptrace.WithClientTrace(req.Context(), o.trace())), o.finish
}

func newExchangeObserver(transport http.RoundTripper) *exchangeObserver {
	var id [16]byte
	// crypto/rand.Read is fail-stop on entropy failure; never send a predictable ID.
	rand.Read(id[:])
	o := &exchangeObserver{start: time.Now(), d: domain.HTTPExchangeDiagnostic{Version: 1, ClientRequestID: "fdy_" + hex.EncodeToString(id[:]), FirstByteMillis: -1, ProxyMode: "unknown", PeerClass: "unknown"}}
	if tr, ok := transport.(*http.Transport); ok {
		o.d.ProxyMode = "configured"
		if tr.Proxy == nil {
			o.d.ProxyMode = "disabled"
		}
	}
	return o
}

func (o *exchangeObserver) record(f func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.frozen {
		f()
	}
}

func (o *exchangeObserver) begin(i int) {
	o.record(func() {
		p := &o.phases[i]
		if p.count == 0 {
			p.start = time.Now()
		}
		p.count++
	})
}

func (o *exchangeObserver) end(i int, err error) {
	o.record(func() { o.endLocked(i, err) })
}

func (o *exchangeObserver) endLocked(i int, err error) {
	p := &o.phases[i]
	p.end, p.done, p.ok = time.Now(), p.done+1, p.ok || err == nil
}

func boundedMillis(d time.Duration) int64 { return min(max(d.Milliseconds(), 0), 120000) }

func (o *exchangeObserver) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { o.begin(0) },
		DNSDone:           func(info httptrace.DNSDoneInfo) { o.end(0, info.Err) },
		ConnectStart:      func(string, string) { o.begin(1) },
		ConnectDone:       func(_, _ string, err error) { o.end(1, err) },
		TLSHandshakeStart: func() { o.begin(2) },
		TLSHandshakeDone: func(s tls.ConnectionState, err error) {
			o.record(func() {
				o.endLocked(2, err)
				if err != nil {
					return
				}
				o.d.TLSVersion = "other"
				switch s.Version {
				case tls.VersionTLS12:
					o.d.TLSVersion = "tls12"
				case tls.VersionTLS13:
					o.d.TLSVersion = "tls13"
				}
				o.d.ALPN = "other"
				switch s.NegotiatedProtocol {
				case "":
					o.d.ALPN = "none"
				case "http/1.1":
					o.d.ALPN = "http1_1"
				case "h2":
					o.d.ALPN = "h2"
				}
				if s.HandshakeComplete && len(s.VerifiedChains) > 0 && len(s.VerifiedChains[0]) > 0 {
					o.d.TLSVerified = true
					leaf := sha256.Sum256(s.VerifiedChains[0][0].Raw)
					o.d.PeerCertificateSHA256 = hex.EncodeToString(leaf[:])
					if len(s.VerifiedChains[0]) > 1 {
						issuer := sha256.Sum256(s.VerifiedChains[0][1].Raw)
						o.d.IssuerSHA256 = hex.EncodeToString(issuer[:])
					}
				}
			})
		},
		GotConn: func(info httptrace.GotConnInfo) {
			o.record(func() {
				o.d.ReusedConnection = info.Reused
				if info.Conn == nil || info.Conn.RemoteAddr() == nil {
					return
				}
				host, _, err := net.SplitHostPort(info.Conn.RemoteAddr().String())
				ip := net.ParseIP(host)
				o.d.PeerClass = "other"
				if err != nil || ip == nil {
					return
				}
				switch {
				case ip.IsLoopback():
					o.d.PeerClass = "loopback"
				case ip.IsPrivate() || ip.IsLinkLocalUnicast():
					o.d.PeerClass = "private"
				default:
					o.d.PeerClass = "public"
				}
			})
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			o.record(func() {
				o.d.RequestWrites = min(o.d.RequestWrites+1, 32)
				o.d.WriteFailed = o.d.WriteFailed || info.Err != nil
			})
		},
		GotFirstResponseByte: func() {
			o.record(func() {
				if o.d.FirstByteMillis < 0 {
					o.d.FirstByteMillis = boundedMillis(time.Since(o.start))
				}
			})
		},
	}
}

func (o *exchangeObserver) finish(response *http.Response) domain.HTTPExchangeDiagnostic {
	if o == nil {
		return domain.HTTPExchangeDiagnostic{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.frozen = true
	now := time.Now()
	phases := [3]*domain.HTTPPhaseDiagnostic{&o.d.DNS, &o.d.Connect, &o.d.TLS}
	for i, p := range o.phases {
		if p.count == 0 {
			continue
		}
		state, end := "incomplete", now
		if p.done == p.count {
			state, end = "failed", p.end
			if p.ok {
				state = "completed"
			}
		}
		*phases[i] = domain.HTTPPhaseDiagnostic{State: state, Attempts: min(p.count, 32), Millis: boundedMillis(end.Sub(p.start))}
	}
	o.d.Headers = domain.HTTPPhaseDiagnostic{State: "failed", Attempts: 1, Millis: boundedMillis(now.Sub(o.start))}
	if response != nil {
		o.d.Headers.State = "completed"
		o.d.FinalStatus = response.StatusCode
		o.d.HeaderFields = min(len(response.Header), 4096)
		o.d.Transport = observeHTTPTransport(response)
	}
	if !o.d.Valid() {
		return domain.HTTPExchangeDiagnostic{}
	}
	return o.d
}
