package acceptance

import (
	"context"
	"encoding/json"
	"errors"
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

func TestInferenceBudgetLoopbackTransport(t *testing.T) {
	for _, kind := range []string{"complete", "missing-media", "abrupt-eof"} {
		t.Run(kind, func(t *testing.T) {
			s, db, a, r, c, _, _, _ := inferenceController(t)
			var dispatches atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.DisableKeepAlives = true
			transport.ForceAttemptHTTP2 = false
			transport.Proxy = nil
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
