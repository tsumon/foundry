package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionRenewalTrustedDesktopOnly(t *testing.T) {
	for _, tc := range []struct {
		platform                        string
		desktop, coding, readonly, want bool
	}{{"darwin", true, true, false, true}, {"windows", true, true, false, true}, {"linux", true, true, false, false}, {"darwin", false, true, false, false}, {"darwin", true, false, false, false}, {"windows", true, true, true, false}} {
		_, p, a, _ := operatorFixture(t, "valid")
		defer p.acceptance.Close()
		c := newDesktopSessionRenewal(&p, tc.platform, tc.desktop, tc.coding, tc.readonly, time.Now)
		if (c != nil) != tc.want {
			t.Fatalf("wrong renewal capability %+v", tc)
		}
		if c != nil {
			status, err := c.Status(a.ID)
			if err != nil || status.Enabled {
				t.Fatalf("default: %+v %v", status, err)
			}
			c.Tick(context.Background())
			c.Close()
		}
	}
}

func TestDesktopOpenAIOnlyTrustedDesktopCoding(t *testing.T) {
	for _, tc := range []struct {
		name, platform            string
		desktop, coding, readonly bool
		want                      bool
	}{{"mac", "darwin", true, true, false, true}, {"windows", "windows", true, true, false, true}, {"linux", "linux", true, true, false, false}, {"cli", "darwin", false, true, false, false}, {"normal", "darwin", true, false, false, false}, {"recovery", "darwin", true, true, true, false}, {"windows-cli", "windows", false, true, false, false}, {"windows-normal", "windows", true, false, false, false}, {"windows-recovery", "windows", true, true, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			db, p, a, r := operatorFixture(t, "valid")
			defer p.acceptance.Close()
			calls := 0
			root := t.TempDir()
			c := newDesktopOpenAI(db, &p, root, tc.platform, tc.desktop, tc.coding, tc.readonly, openaiConnectionDeps{now: time.Now, probe: func(string) error { t.Fatal("desktop used CLI diagnostic policy"); return errors.New("forbidden") }, desktopProbe: func(dataDir string) error {
				if dataDir != root {
					t.Fatal("desktop diagnostic root changed")
				}
				calls++
				return nil
			}})
			if (c != nil) != tc.want {
				t.Fatalf("capability=%v", c != nil)
			}
			if calls != 0 {
				t.Fatal("startup probed")
			}
			if c != nil {
				defer c.Close()
				if e := c.Prepare(a.ID, "connection", r.ID, a.Revision); e != nil || calls != 1 {
					t.Fatalf("explicit prepare %v %d", e, calls)
				}
			}
		})
	}
}
func TestDesktopOpenAIDiagnosticFailureStaysClosed(t *testing.T) {
	db, p, a, r := operatorFixture(t, "valid")
	defer p.acceptance.Close()
	dir := t.TempDir()
	calls := 0
	c := newDesktopOpenAI(db, &p, dir, "darwin", true, true, false, openaiConnectionDeps{now: time.Now, desktopProbe: func(string) error { calls++; return errors.New("diagnostic cleanup unresolved") }})
	defer c.Close()
	if c.Status(a.ID).ConnectionReady || calls != 0 {
		t.Fatal("restart performed native check")
	}
	if e := c.Prepare(a.ID, "connection", r.ID, a.Revision); e == nil || calls != 1 {
		t.Fatal("unconfirmed prior cleanup bypassed")
	}
	if c.Status(a.ID).Blocker != "diagnostic_failed" || c.Status(a.ID).ConnectionReady {
		t.Fatal("failed diagnostic granted readiness")
	}
	if e := c.Prepare(a.ID, "connection", r.ID, a.Revision); e == nil || calls != 1 {
		t.Fatal("failed diagnostic automatically retried")
	}
}
