package acceptance

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopPreparationAuthority(t *testing.T) {
	_, db, opts, a, _ := productionController(t, func() bool { return true })
	var probes atomic.Int32
	now := time.Now()
	c := NewDesktopCoordinator(db, DesktopOptions{SIWC: opts.SIWC, Resolver: &catalogResolver{}, Probe: func() error { probes.Add(1); return nil }, Now: func() time.Time { return now }})
	defer c.Close()
	if c.Status(a.ID).Phase != "unprepared" || probes.Load() != 0 {
		t.Fatal("startup performed effects")
	}
	if err := c.Prepare(a.ID, "connection", opts.Route.ID, a.Revision); err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 1 || !c.Status(a.ID).ConnectionReady {
		t.Fatal("explicit preparation missing")
	}
	var old domain.ModelAcceptanceOperation
	c.WithService(func(s *Service) {
		var err error
		old, err = s.Propose(a.ID, productionProposal(a, opts.Route))
		if err != nil {
			t.Fatal(err)
		}
	})
	if probes.Load() != 1 {
		t.Fatal("metadata or proposal probed")
	}
	now = now.Add(11 * time.Minute)
	if c.Status(a.ID).ConnectionReady {
		t.Fatal("expired lease renewed by metadata")
	}
	c.WithService(func(s *Service) {
		if _, err := s.Decide(a.ID, old.ID, old.Revision, old.MaterialDigest, "approve"); err == nil {
			t.Fatal("expired lease approved")
		}
	})
	if err := c.Prepare(a.ID, "connection", opts.Route.ID, a.Revision); err != nil {
		t.Fatal(err)
	}
	c.WithService(func(s *Service) {
		if _, err := s.Decide(a.ID, old.ID, old.Revision, old.MaterialDigest, "approve"); err == nil {
			t.Fatal("stale generation approved")
		}
	})
	if probes.Load() != 2 {
		t.Fatal("fresh preparation did not use fresh diagnostic")
	}
	c.Close()
	restarted := NewDesktopCoordinator(db, DesktopOptions{SIWC: opts.SIWC, Resolver: &catalogResolver{}, Probe: func() error { probes.Add(1); return nil }})
	defer restarted.Close()
	if restarted.Status(a.ID).Phase != "unprepared" || probes.Load() != 2 {
		t.Fatal("restart restored authority or probed")
	}
}

func TestDesktopPreparationRejectsUnknownRouteAndCleanupFailure(t *testing.T) {
	_, db, opts, a, _ := productionController(t, func() bool { return true })
	calls := 0
	c := NewDesktopCoordinator(db, DesktopOptions{SIWC: opts.SIWC, Resolver: &catalogResolver{}, Probe: func() error { calls++; return errors.New("synthetic cleanup uncertain") }})
	defer c.Close()
	for _, phase := range []string{"inference", "connection"} {
		if err := c.Prepare(a.ID, phase, "not-owned", a.Revision); err == nil {
			t.Fatal("untrusted route accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid route probed")
	}
	if err := c.Prepare(a.ID, "connection", opts.Route.ID, a.Revision); err == nil {
		t.Fatal("uncertain cleanup granted authority")
	}
	if c.Status(a.ID).Blocker != "diagnostic_failed" || c.Status(a.ID).ConnectionReady {
		t.Fatal("failure not visible")
	}
	if err := c.Prepare(a.ID, "catalog", opts.Route.ID, a.Revision); err == nil || calls != 1 {
		t.Fatal("failure bypass or automatic retry")
	}
}

func TestDesktopConcurrentPreparationAndActiveSession(t *testing.T) {
	_, db, opts, a, _ := productionController(t, func() bool { return true })
	entered, release := make(chan struct{}), make(chan struct{})
	c := NewDesktopCoordinator(db, DesktopOptions{SIWC: opts.SIWC, Resolver: &catalogResolver{}, Probe: func() error { close(entered); <-release; return nil }})
	defer c.Close()
	done := make(chan error, 1)
	go func() { done <- c.Prepare(a.ID, "connection", opts.Route.ID, a.Revision) }()
	<-entered
	if err := c.Prepare(a.ID, "connection", opts.Route.ID, a.Revision); err == nil {
		t.Fatal("concurrent preparation admitted")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var running domain.ModelAcceptanceOperation
	c.WithService(func(s *Service) {
		o, e := s.Propose(a.ID, productionProposal(a, opts.Route))
		if e != nil {
			t.Fatal(e)
		}
		o, e = s.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
		if e != nil {
			t.Fatal(e)
		}
		running, e = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
		if e != nil || running.State != "running" {
			t.Fatalf("start: %s %v", running.State, e)
		}
	})
	var link string
	c.WithService(func(s *Service) {
		_, session, e := s.Read(a.ID, running.ID)
		if e != nil || session == nil {
			t.Fatal("missing session")
		}
		link = session.HandoffURL
	})
	if !c.Handoff(link) || c.Handoff("http://127.0.0.1:1/auth/start") || c.Handoff(link+"?state=forged") {
		t.Fatal("handoff not bound to current owned session")
	}
	if err := c.Prepare(a.ID, "catalog", opts.Route.ID, a.Revision); err == nil {
		t.Fatal("active callback orphaned")
	}
	c.WithService(func(s *Service) {
		if _, e := s.Cancel(a.ID, running.ID, running.Revision, running.MaterialDigest); e != nil {
			t.Fatal(e)
		}
	})
	if c.Handoff(link) {
		t.Fatal("canceled session restored browser link")
	}
}

func TestDesktopCatalogRealServiceFailuresNeverRetryOrRefresh(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body, state string
	}{{"success", 200, `{"models":[{"slug":"gpt-5.1","display_name":"Synthetic","visibility":"list"}]}`, "succeeded"}, {"429", 429, `{}`, "failed"}, {"500", 500, `{}`, "failed"}, {"malformed", 200, `{`, "unknown"}, {"transport", 0, ``, "unknown"}} {
		t.Run(tc.name, func(t *testing.T) {
			prior, db, a, r, connection, resolver, calls := catalogController(t)
			c := NewDesktopCoordinator(db, DesktopOptions{Resolver: resolver, Probe: func() error { t.Fatal("catalog probed native storage"); return nil }})
			defer c.Close()
			if e := c.Prepare(a.ID, "catalog", r.ID, a.Revision); e != nil {
				t.Fatal(e)
			}
			c.WithService(func(s *Service) {
				s.catalog.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
					calls.Add(1)
					if req.Method != "GET" || req.URL.String() != domain.OpenAICatalogURL || req.Body != nil {
						t.Error("catalog authority exceeded")
					}
					if tc.status == 0 {
						return nil, errors.New("synthetic transport failure")
					}
					return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
				})
			})
			if !c.Status(a.ID).CatalogReady || calls.Load() != 0 || resolver.reads.Load() != 0 {
				t.Fatal("preparation performed external effects")
			}
			var approved domain.ModelAcceptanceOperation
			c.WithService(func(s *Service) { approved = approveCatalog(t, s, proposeCatalog(t, s, a, r, connection)) })
			// A request using the prior process service cannot adopt this generation.
			if _, e := prior.Decide(a.ID, approved.ID, approved.Revision, approved.MaterialDigest, "approve"); e == nil {
				t.Fatal("old admission adopted coordinator material")
			}
			c.WithService(func(s *Service) {
				got, e := s.Execute(context.Background(), a.ID, approved.ID, approved.Revision, approved.MaterialDigest)
				if e != nil || got.State != tc.state {
					t.Fatalf("receipt %s %v", got.State, e)
				}
				if _, e = s.Execute(context.Background(), a.ID, approved.ID, approved.Revision, approved.MaterialDigest); e == nil {
					t.Fatal("duplicate executed")
				}
			})
			if calls.Load() != 1 || resolver.reads.Load() != 1 {
				t.Fatalf("not single dispatch: calls=%d reads=%d", calls.Load(), resolver.reads.Load())
			}
		})
	}
}
