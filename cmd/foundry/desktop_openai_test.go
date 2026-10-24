package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDesktopOpenAIOnlyTrustedMacCoding(t *testing.T) {
	for _, tc := range []struct {
		name, platform            string
		desktop, coding, readonly bool
		want                      bool
	}{{"mac", "darwin", true, true, false, true}, {"windows", "windows", true, true, false, false}, {"linux", "linux", true, true, false, false}, {"cli", "darwin", false, true, false, false}, {"normal", "darwin", true, false, false, false}, {"recovery", "darwin", true, true, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			db, p, a, r := operatorFixture(t, "valid")
			defer p.acceptance.Close()
			calls := 0
			c := newDesktopOpenAI(db, &p, t.TempDir(), tc.platform, tc.desktop, tc.coding, tc.readonly, openaiConnectionDeps{now: time.Now, probe: func(string) error { calls++; return nil }})
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
func TestDesktopOpenAIRestartRetainsCleanupBlocker(t *testing.T) {
	db, p, a, r := operatorFixture(t, "valid")
	defer p.acceptance.Close()
	dir := t.TempDir()
	journalDir := filepath.Join(dir, "openai-desktop-diagnostics")
	if e := os.Mkdir(journalDir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(journalDir, "synthetic-incomplete.json"), []byte(`{"cleanup":"unconfirmed"}`), 0600); e != nil {
		t.Fatal(e)
	}
	calls := 0
	c := newDesktopOpenAI(db, &p, dir, "darwin", true, true, false, openaiConnectionDeps{now: time.Now, probe: func(string) error { calls++; return nil }})
	defer c.Close()
	if c.Status(a.ID).ConnectionReady || calls != 0 {
		t.Fatal("restart performed native check")
	}
	if e := c.Prepare(a.ID, "connection", r.ID, a.Revision); e == nil || calls != 0 {
		t.Fatal("unconfirmed prior cleanup bypassed")
	}
}
