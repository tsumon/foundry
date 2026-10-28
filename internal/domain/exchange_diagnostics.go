package domain

import "strings"

// HTTPPhaseDiagnostic records bounded elapsed time, never error messages or
// addresses. Zero means unobserved. Attempts/time saturate at 32/120000ms.
type HTTPPhaseDiagnostic struct {
	State    string `json:"state,omitempty"`
	Attempts int    `json:"attempts"`
	Millis   int64  `json:"millis"`
}

func (p HTTPPhaseDiagnostic) Valid() bool {
	if p == (HTTPPhaseDiagnostic{}) {
		return true
	}
	return (p.State == "completed" || p.State == "failed" || p.State == "incomplete") && p.Attempts >= 1 && p.Attempts <= 32 && p.Millis >= 0 && p.Millis <= 120000
}

// HTTPExchangeDiagnostic is additive evidence from a single existing request.
// It neither authorizes dispatch nor proves which party caused an omission.
// Fingerprints refer to public certificates, not credentials or TLS secrets.
type HTTPExchangeDiagnostic struct {
	Version               int                     `json:"version"`
	ClientRequestID       string                  `json:"client_request_id"`
	DNS                   HTTPPhaseDiagnostic     `json:"dns"`
	Connect               HTTPPhaseDiagnostic     `json:"connect"`
	TLS                   HTTPPhaseDiagnostic     `json:"tls"`
	Headers               HTTPPhaseDiagnostic     `json:"headers"`
	FirstByteMillis       int64                   `json:"first_byte_millis"`
	RequestWrites         int                     `json:"request_writes"`
	WriteFailed           bool                    `json:"write_failed"`
	ReusedConnection      bool                    `json:"reused_connection"`
	ProxyMode             string                  `json:"proxy_mode"`
	PeerClass             string                  `json:"peer_class"`
	TLSVersion            string                  `json:"tls_version,omitempty"`
	ALPN                  string                  `json:"alpn,omitempty"`
	TLSVerified           bool                    `json:"tls_verified"`
	PeerCertificateSHA256 string                  `json:"peer_certificate_sha256,omitempty"`
	IssuerSHA256          string                  `json:"issuer_sha256,omitempty"`
	FinalStatus           int                     `json:"final_status"`
	HeaderFields          int                     `json:"header_fields"`
	Transport             HTTPTransportDiagnostic `json:"transport,omitzero"`
}

func lowerHex(s string, size int) bool {
	if len(s) != size {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (d HTTPExchangeDiagnostic) Valid() bool {
	if d.Version != 1 || !strings.HasPrefix(d.ClientRequestID, "fdy_") || !lowerHex(strings.TrimPrefix(d.ClientRequestID, "fdy_"), 32) {
		return false
	}
	for _, p := range []HTTPPhaseDiagnostic{d.DNS, d.Connect, d.TLS, d.Headers} {
		if !p.Valid() {
			return false
		}
	}
	if d.Headers.Attempts != 1 || d.FirstByteMillis < -1 || d.FirstByteMillis > 120000 || d.RequestWrites < 0 || d.RequestWrites > 32 || d.WriteFailed && d.RequestWrites == 0 || d.HeaderFields < 0 || d.HeaderFields > 4096 {
		return false
	}
	if d.Headers.State == "failed" {
		if d.FinalStatus != 0 || d.Transport != (HTTPTransportDiagnostic{}) || d.HeaderFields != 0 {
			return false
		}
	} else if d.FinalStatus < 0 || d.FinalStatus > 999 || !d.Transport.Valid() || d.Headers.State != "completed" {
		return false
	}
	switch d.ProxyMode {
	case "disabled", "configured", "unknown":
	default:
		return false
	}
	switch d.PeerClass {
	case "unknown", "loopback", "private", "public", "other":
	default:
		return false
	}
	switch d.TLSVersion {
	case "", "tls12", "tls13", "other":
	default:
		return false
	}
	switch d.ALPN {
	case "", "none", "http1_1", "h2", "other":
	default:
		return false
	}
	if d.TLS.State != "completed" {
		return d.TLSVersion == "" && d.ALPN == "" && !d.TLSVerified && d.PeerCertificateSHA256 == "" && d.IssuerSHA256 == ""
	}
	if d.TLSVersion == "" || d.ALPN == "" {
		return false
	}
	if !d.TLSVerified {
		return d.PeerCertificateSHA256 == "" && d.IssuerSHA256 == ""
	}
	return lowerHex(d.PeerCertificateSHA256, 64) && (d.IssuerSHA256 == "" || lowerHex(d.IssuerSHA256, 64))
}
