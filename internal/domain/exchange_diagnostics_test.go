package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExchangeDiagnosticRejectsUnboundedOrUntrustedValues(t *testing.T) {
	valid := HTTPExchangeDiagnostic{Version: 1, ClientRequestID: "fdy_0123456789abcdef0123456789abcdef", ProxyMode: "disabled", PeerClass: "loopback", Headers: HTTPPhaseDiagnostic{State: "completed", Attempts: 1}, FirstByteMillis: 0, FinalStatus: 200, RequestWrites: 1, HeaderFields: 2, Transport: HTTPTransportDiagnostic{Version: 1, ContentType: "absent", Protocol: "http1_1", BodyLength: "unknown"}}
	if !valid.Valid() {
		t.Fatal("valid observation rejected")
	}
	for name, mutate := range map[string]func(*HTTPExchangeDiagnostic){
		"version":                          func(d *HTTPExchangeDiagnostic) { d.Version = 99 },
		"identity":                         func(d *HTTPExchangeDiagnostic) { d.ClientRequestID = "PRIVATE" },
		"identity suffix":                  func(d *HTTPExchangeDiagnostic) { d.ClientRequestID += "x" },
		"dns error":                        func(d *HTTPExchangeDiagnostic) { d.DNS = HTTPPhaseDiagnostic{State: "PRIVATE", Attempts: 1} },
		"negative duration":                func(d *HTTPExchangeDiagnostic) { d.Headers.Millis = -1 },
		"long duration":                    func(d *HTTPExchangeDiagnostic) { d.Headers.Millis = 120001 },
		"attempts":                         func(d *HTTPExchangeDiagnostic) { d.Connect = HTTPPhaseDiagnostic{State: "completed", Attempts: 33} },
		"proxy url":                        func(d *HTTPExchangeDiagnostic) { d.ProxyMode = "http://PRIVATE" },
		"peer ip":                          func(d *HTTPExchangeDiagnostic) { d.PeerClass = "10.1.2.3" },
		"tls version":                      func(d *HTTPExchangeDiagnostic) { d.TLSVersion = "PRIVATE" },
		"alpn":                             func(d *HTTPExchangeDiagnostic) { d.ALPN = "PRIVATE" },
		"certificate without TLS":          func(d *HTTPExchangeDiagnostic) { d.PeerCertificateSHA256 = strings.Repeat("a", 64) },
		"verification without TLS":         func(d *HTTPExchangeDiagnostic) { d.TLSVerified = true },
		"write error without attempt":      func(d *HTTPExchangeDiagnostic) { d.RequestWrites = 0; d.WriteFailed = true },
		"header overflow":                  func(d *HTTPExchangeDiagnostic) { d.HeaderFields = 4097 },
		"negative first byte":              func(d *HTTPExchangeDiagnostic) { d.FirstByteMillis = -2 },
		"unobserved response with headers": func(d *HTTPExchangeDiagnostic) { d.Headers.State = "failed" },
		"unknown status":                   func(d *HTTPExchangeDiagnostic) { d.FinalStatus = 1000 },
		"header contents":                  func(d *HTTPExchangeDiagnostic) { d.Transport.ContentType = "PRIVATE" },
	} {
		t.Run(name, func(t *testing.T) {
			d := valid
			mutate(&d)
			if d.Valid() {
				t.Fatal("unsafe observation accepted")
			}
			r := SanitizeModelCallReceipt(ModelCallReceipt{Certainty: "unknown", ExchangeDiagnostic: d})
			if r.ExchangeDiagnostic != (HTTPExchangeDiagnostic{}) {
				t.Fatal("unsafe observation retained")
			}
		})
	}
	legacy := ModelCallReceipt{Certainty: "unknown", HTTPStatus: 200}
	if SanitizeModelCallReceipt(legacy) != legacy {
		t.Fatal("historical receipt changed")
	}
	raw, _ := json.Marshal(legacy)
	if strings.Contains(string(raw), "exchange_diagnostic") {
		t.Fatal("invented old transport observation")
	}
}
