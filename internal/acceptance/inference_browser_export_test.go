package acceptance

// This export bridge exists only in go test's augmented package. Normal builds
// cannot replace inference transport, clocks, secrets, or evidence.
import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/store"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type InferenceBrowserFixture struct {
	Service   *Service
	Store     *store.Store
	Account   domain.ModelAccount
	Operation domain.ModelAcceptanceOperation
	Counters  func() (int32, int32)
}

func NewInferenceBrowserFixture(t *testing.T, scenario string) InferenceBrowserFixture {
	t.Helper()
	s, db, a, r, c, resolver, calls, _ := inferenceController(t)
	body := inferenceCompletion
	switch scenario {
	case "pending", "approved", "success", "expired", "default-closed":
	case "quota":
		body = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"FOUNDRY_INFERENCE_OK\"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"object\":\"response\",\"status\":\"failed\",\"error\":{\"code\":\"subscription_sharing_usage_limit_exceeded\"}}}\n\n"
	case "interrupted":
		body = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"FOUNDRY_INFERENCE_OK\"}\n\n"
	default:
		t.Fatalf("unsupported local scenario %q", scenario)
	}
	s.inference.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		raw, err := io.ReadAll(req.Body)
		if err != nil || req.Method != "POST" || req.URL.String() != domain.OpenAIInferenceURL || string(raw) != domain.OpenAIInferenceBody || req.Header.Get("Authorization") != "Bearer synthetic-catalog-secret" {
			t.Error("local fixture request exceeded fixed authority")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"req_local_simulation"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	o := proposeInference(t, s, a, r, c)
	if scenario != "pending" {
		o = approveCatalog(t, s, o)
	}
	if scenario == "expired" {
		// Only the private service clock advances; no real credential is modified.
		future := time.Now().Add(2 * time.Hour)
		s.now = func() time.Time { return future }
	}
	if scenario == "default-closed" {
		s = New(db)
		if err := s.Recover(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
	}
	return InferenceBrowserFixture{Service: s, Store: db, Account: a, Operation: o, Counters: func() (int32, int32) { return resolver.reads.Load(), calls.Load() }}
}

func TestInferenceBrowserFixtureOutcomes(t *testing.T) {
	for _, scenario := range []string{"pending", "approved", "success", "quota", "interrupted", "expired", "default-closed"} {
		t.Run(scenario, func(t *testing.T) {
			f := NewInferenceBrowserFixture(t, scenario)
			if reads, sends := f.Counters(); reads != 0 || sends != 0 {
				t.Fatal("setup dispatched")
			}
			o := f.Operation
			if scenario == "pending" {
				if _, err := f.Service.Execute(context.Background(), o.AccountID, o.ID, o.Revision, o.MaterialDigest); err == nil {
					t.Fatal("pending executed")
				}
				return
			}
			result, err := f.Service.Execute(context.Background(), o.AccountID, o.ID, o.Revision, o.MaterialDigest)
			if scenario == "expired" || scenario == "default-closed" {
				if err == nil {
					t.Fatal("closed fixture executed")
				}
				if reads, sends := f.Counters(); reads != 0 || sends != 0 {
					t.Fatal("closed fixture touched external boundary")
				}
				return
			}
			want := map[string]string{"approved": "succeeded", "success": "succeeded", "quota": "failed", "interrupted": "unknown"}[scenario]
			if err != nil || result.State != want || result.Attempts != 1 {
				t.Fatalf("outcome %s %v", result.State, err)
			}
			if scenario == "quota" && result.Code != "quota_exhausted" {
				t.Fatal("quota fixture not classified")
			}
			if reads, sends := f.Counters(); reads != 1 || sends != 1 {
				t.Fatal("not one dispatch")
			}
			if _, err := f.Service.Execute(context.Background(), o.AccountID, o.ID, o.Revision, o.MaterialDigest); err == nil {
				t.Fatal("replayed")
			}
		})
	}
}
