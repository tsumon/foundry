package acceptance

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Exercise the real production HTTP client against hand-written loopback TLS
// responses. Header case, decompression and chunking must not erase media type;
// neither informational headers, trailers nor a valid body can supply a missing
// final Content-Type. Each temporary task pays for exactly one synthetic send.
func TestInferenceWireMediaBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, head, framing, interim, trailer, contentType, reason string
		compressed                                                 bool
	}{
		{name: "lowercase", head: "content-type: text/event-stream\r\n", contentType: "single"},
		{name: "mixed case", head: "cOnTeNt-TyPe: TEXT/EVENT-STREAM; charset=utf-8\r\n", contentType: "single"},
		{name: "gzip", head: "Content-Type: text/event-stream\r\n", compressed: true, contentType: "single"},
		{name: "chunked", head: "Content-Type: text/event-stream\r\n", framing: "chunked", contentType: "single"},
		{name: "missing", contentType: "absent", reason: "missing_media_type"},
		{name: "gzip missing", compressed: true, contentType: "absent", reason: "missing_media_type"},
		{name: "chunked missing", framing: "chunked", contentType: "absent", reason: "missing_media_type"},
		{name: "informational only", interim: "HTTP/1.1 103 Early Hints\r\nContent-Type: text/event-stream\r\n\r\n", contentType: "absent", reason: "missing_media_type"},
		{name: "trailer only", framing: "chunked", head: "Trailer: Content-Type\r\n", trailer: "Content-Type: text/event-stream\r\n", contentType: "absent", reason: "missing_media_type"},
		{name: "SSE first conflicting", head: "Content-Type: text/event-stream\r\nContent-Type: application/json\r\n", contentType: "multiple", reason: "invalid_media_type"},
		{name: "repeated SSE", head: "Content-Type: text/event-stream\r\nContent-Type: text/event-stream\r\n", contentType: "multiple", reason: "invalid_media_type"},
		{name: "empty first duplicate", head: "Content-Type:\r\nContent-Type: text/event-stream\r\n", contentType: "multiple", reason: "invalid_media_type"},
		{name: "combined duplicate", head: "Content-Type: text/event-stream, text/event-stream\r\n", contentType: "single", reason: "invalid_media_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, a, r, c, resolver, _, _ := inferenceController(t)
			var dispatches, dials atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				dispatches.Add(1)
				body, err := io.ReadAll(req.Body)
				if err != nil || string(body) != domain.OpenAIInferenceBody || req.Method != "POST" || req.URL.Path != "/v1/responses" || req.Header.Get("Content-Type") != "application/json" || req.Header.Get("Accept") != "text/event-stream" || req.Header.Get("Authorization") != "Bearer synthetic-catalog-secret" {
					t.Error("synthetic request contract changed")
				}
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				payload, head := inferenceCompletion, tc.head
				if tc.compressed {
					var compressed strings.Builder
					zip := gzip.NewWriter(&compressed)
					if _, err = io.WriteString(zip, payload); err != nil {
						t.Error(err)
						return
					}
					if err = zip.Close(); err != nil {
						t.Error(err)
						return
					}
					payload, head = compressed.String(), head+"Content-Encoding: gzip\r\n"
				}
				if tc.framing == "chunked" {
					head += "Transfer-Encoding: chunked\r\n"
					payload = fmt.Sprintf("%x\r\n%s\r\n0\r\n%s\r\n", len(payload), payload, tc.trailer)
				} else {
					head += fmt.Sprintf("Content-Length: %d\r\n", len(payload))
				}
				fmt.Fprintf(rw, "%sHTTP/1.1 200 OK\r\n%sConnection: close\r\n\r\n%s", tc.interim, head, payload)
				if err = rw.Flush(); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			production := NewProductionInference(db, ProductionInferenceOptions{Route: r, Resolver: resolver})
			transport := production.inference.http.Transport.(*http.Transport).Clone()
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
				if address != "api.openai.com:443" {
					return nil, errors.New("unexpected synthetic destination")
				}
				dials.Add(1)
				return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
			}
			defer transport.CloseIdleConnections()
			s.inference.http.Transport = transport
			o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
			got, err := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
			if err != nil || got.InferenceDiagnostic == nil {
				t.Fatalf("missing receipt: %v", err)
			}
			d := got.InferenceDiagnostic
			if !d.Valid() || !d.Exchange.TLSVerified || d.Exchange.Transport.Protocol != "http1_1" || d.Exchange.Transport.ContentType != tc.contentType || d.Exchange.RequestWrites != 1 {
				t.Fatalf("incorrect boundary observation: %+v", d)
			}
			if tc.reason == "" {
				if got.State != "succeeded" || got.OutputMatched == nil || !*got.OutputMatched {
					t.Fatalf("valid response rejected: %s %s", got.State, got.Code)
				}
			} else if got.State != "unknown" || got.Code != "response_incomplete" || got.OutputMatched != nil || got.UsageKnown || d.Response == nil || d.Response.Reason != tc.reason || d.Response.BytesRead != 0 || d.Response.EventsSeen != 0 {
				t.Fatalf("ambiguous/missing media accepted or parsed: state=%s code=%s diagnostic=%+v", got.State, got.Code, d.Response)
			}
			if _, err = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err == nil {
				t.Fatal("consumed approval replayed")
			}
			if dispatches.Load() != 1 || dials.Load() != 1 || resolver.reads.Load() != 1 {
				t.Fatal("unexpected replay or credential access")
			}
			mission, err := db.GetMission("inference-budget-task")
			if err != nil || mission.Budget.UsedCalls != 4 || mission.Budget.MaxCalls != 7 {
				t.Fatal("wrong synthetic budget charge")
			}
		})
	}
}

func TestInferenceBudgetLoopbackTransport(t *testing.T) {
	for _, kind := range []string{"complete", "missing-media", "abrupt-eof"} {
		t.Run(kind, func(t *testing.T) {
			s, db, a, r, c, _, _, _ := inferenceController(t)
			var dispatches atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				dispatches.Add(1)
				body, err := io.ReadAll(req.Body)
				if err != nil || string(body) != domain.OpenAIInferenceBody || req.Header.Get("X-Client-Request-Id") == "" {
					t.Error("request contract changed")
				}
				if kind == "missing-media" {
					w.Header()["Content-Type"] = nil
				} else {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				if kind == "abrupt-eof" {
					w.Header().Set("Content-Length", "99999")
					io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
					return
				}
				io.WriteString(w, inferenceCompletion)
			}))
			server.EnableHTTP2 = true
			server.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
			server.StartTLS()
			defer server.Close()
			production := NewProductionInference(db, ProductionInferenceOptions{Route: r, Resolver: s.inference.resolver})
			transport := production.inference.http.Transport.(*http.Transport).Clone()
			// Change only the trust root and destination to the owned TLS fixture;
			// retain the production constructor's protocol and replay policy.
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
			}
			defer transport.CloseIdleConnections()
			s.inference.http.Transport = transport
			o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
			got, err := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
			if err != nil || dispatches.Load() != 1 || got.InferenceDiagnostic == nil {
				t.Fatalf("missing real observation: %+v %v", got, err)
			}
			d := got.InferenceDiagnostic
			if !d.Valid() || !d.Exchange.TLSVerified || d.Exchange.PeerClass != "loopback" || d.Exchange.RequestWrites != 1 || d.Exchange.Transport.Protocol != "http1_1" {
				t.Fatalf("bad transport: %+v", d)
			}
			if kind == "complete" {
				if got.State != "succeeded" || got.OutputMatched == nil || !*got.OutputMatched {
					t.Fatal("completion lost")
				}
			} else if got.State != "unknown" || got.OutputMatched != nil || d.Response == nil {
				t.Fatal("bad failed projection", got.State)
			}
			if kind == "abrupt-eof" && d.Response.Reason != "read_error" {
				t.Fatal("read error misclassified")
			}
			m, e := db.GetMission("inference-budget-task")
			if e != nil || m.Budget.UsedCalls != 4 || m.Budget.MaxCalls != 7 {
				t.Fatal("wrong debit", m.Budget, e)
			}
		})
	}
}

func TestInferenceBudgetPartialStreamHasBoundedParserDiagnostic(t *testing.T) {
	_, err := model.DecodeSubscriptionInference(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"FOUNDRY_INFERENCE_OK\"}\n\n"), "synthetic-secret")
	var pe *model.ProviderError
	if !errors.As(err, &pe) || !pe.ResponseDiagnostic.Valid() || pe.ResponseDiagnostic.Stage != "stream_read" || pe.ResponseDiagnostic.Reason != "missing_terminal" {
		t.Fatalf("missing parser diagnostic: %+v", pe)
	}
}

func inferenceTaskFixture(t *testing.T, s *store.Store, a domain.ModelAccount, r domain.ModelRoute) {
	t.Helper()
	p := domain.Profile{ID: "diagnostic-profile", Name: "Synthetic", Kind: "direct", Protocol: r.Protocol, BaseURL: r.BaseURL, AccountID: a.ID, RouteID: r.ID, Model: domain.OpenAIInferenceModel}
	if e := s.PutProfile(p); e != nil {
		t.Fatal(e)
	}
	if e := s.PutSettings(domain.Settings{DefaultProfileID: p.ID, RoleBindings: map[domain.Role]string{}}); e != nil {
		t.Fatal(e)
	}
	def, profiles, snapshot, e := s.ResolveMissionModels("")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateMission(domain.Mission{ID: "inference-budget-task", State: "blocked", Epoch: 1, WorkspaceMode: "generations_account_snapshot_v2", Spec: domain.Spec{ProfileID: def}, Profiles: profiles, ModelSnapshot: snapshot, Budget: domain.Budget{UsedCalls: 3, MaxCalls: 7}})
	if e != nil {
		t.Fatal(e)
	}
}

func TestInferenceBudgetMissingMediaRetainsDebitAndDiagnostics(t *testing.T) {
	s, db, a, r, c, _, calls, _ := inferenceController(t)
	s.inference.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(inferenceCompletion)), Request: req}, nil
	})
	o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
	got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if e != nil || got.State != "unknown" || got.OutputMatched != nil || got.UsageKnown || calls.Load() != 1 {
		t.Fatalf("missing media promoted: %+v %v", got, e)
	}
	m, e := db.GetMission("inference-budget-task")
	if e != nil || m.Budget.UsedCalls != 4 || m.Budget.MaxCalls != 7 {
		t.Fatalf("missing debit %+v %v", m.Budget, e)
	}
	raw, _ := json.Marshal(got)
	var projection struct {
		Diagnostics struct {
			Exchange domain.HTTPExchangeDiagnostic `json:"exchange"`
			Response domain.ResponseDiagnostic     `json:"response"`
		} `json:"inference_diagnostic"`
	}
	if e = json.Unmarshal(raw, &projection); e != nil {
		t.Fatal(e)
	}
	if !projection.Diagnostics.Exchange.Valid() || !projection.Diagnostics.Response.Valid() || projection.Diagnostics.Response.Reason != "missing_media_type" || projection.Diagnostics.Exchange.Transport.ContentType != "absent" {
		t.Fatalf("missing bounded diagnostics: %s", raw)
	}
}
