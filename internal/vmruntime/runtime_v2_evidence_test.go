package vmruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeV2AuthenticatedEvidenceRequiresExactClosedOwner(t *testing.T) {
	for _, fault := range []string{"none", "trailing", "unconfirmed", "closed-receipt"} {
		t.Run(fault, func(t *testing.T) {
			r, q := testRuntimeV2(t, "c4_typescript_model_v2")
			r.launch = fakeLaunchV2(fault == "trailing", false)
			if fault == "unconfirmed" {
				r.launch = func(ctx context.Context, c command) (nativeProcess, error) {
					p, e := fakeLaunchV2(false, false)(ctx, c)
					return &unconfirmedNative{p.(*fakeNative)}, e
				}
			}
			s, e := r.CreateV2(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			if _, ok := s.AuthenticatedEvidenceV2(q); ok {
				t.Fatal("unstarted evidence")
			}
			if e = s.Start(context.Background()); e != nil {
				t.Fatal(e)
			}
			waitErr := s.Wait(context.Background())
			if _, ok := s.AuthenticatedEvidenceV2(q); ok {
				t.Fatal("evidence before Close")
			}
			if fault == "closed-receipt" {
				if e = os.Mkdir(filepath.Join(s.dir, "closed.json"), 0700); e != nil {
					t.Fatal(e)
				}
			}
			closeErr := s.Close(context.Background())
			got, ok := s.AuthenticatedEvidenceV2(q)
			if fault != "none" {
				if ok {
					t.Fatal("failed lifecycle exposed evidence")
				}
				if waitErr == nil && closeErr == nil {
					t.Fatal("fault not exercised")
				}
				return
			}
			if waitErr != nil || closeErr != nil || !ok || got.SessionID != s.ID() || got.OperationID != "op" || got.Target.ProfileID != "c4_typescript_model_v2" || !got.Checks.Valid() || len(got.Phases) != 2 || got.Tests != 1 || got.Class != "passed" {
				t.Fatalf("missing evidence: %+v %v %v", got, waitErr, closeErr)
			}
			got.Phases[0].Phase = "caller mutation"
			again, ok := s.AuthenticatedEvidenceV2(q)
			if !ok || again.Phases[0].Phase != "compile-typescript" {
				t.Fatal("evidence aliases session")
			}
			q.Owner.Owner = "foreign"
			if _, ok := s.AuthenticatedEvidenceV2(q); ok {
				t.Fatal("foreign owner evidence")
			}
		})
	}
}
