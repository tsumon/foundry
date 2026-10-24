package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/store"
	"os"
	"path/filepath"
)

// Only the trusted desktop coding composition calls this constructor. The
// platform and dependencies are private test seams, never flags or settings.
func newDesktopOpenAI(s *store.Store, p *productionModels, dataDir, platform string, desktop, coding, readonly bool, deps openaiConnectionDeps) *acceptance.DesktopCoordinator {
	if platform != "darwin" || !desktop || !coding || readonly {
		return nil
	}
	return acceptance.NewDesktopCoordinator(s, acceptance.DesktopOptions{SIWC: p.siwc, Resolver: p.accounts, Now: deps.now, Probe: func() error {
		if deps.probe == nil {
			return errors.New("diagnostic unavailable")
		}
		dir := filepath.Join(dataDir, "openai-desktop-diagnostics")
		if e := os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		// Retain every private journal. Unknown/incomplete cleanup blocks a new
		// probe even after restart; only explicit exact-item operator recovery can
		// resolve it. Never read a credential or replay a native cleanup here.
		entries, e := os.ReadDir(dir)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			info, e := entry.Info()
			if e != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
				return errors.New("diagnostic journal unresolved")
			}
			raw, e := os.ReadFile(filepath.Join(dir, entry.Name()))
			if e != nil {
				return e
			}
			var journal struct {
				Cleanup string `json:"cleanup"`
			}
			if json.Unmarshal(raw, &journal) != nil || journal.Cleanup != "confirmed" {
				return errors.New("diagnostic cleanup unresolved")
			}
		}
		nonce := make([]byte, 16)
		if _, e := rand.Read(nonce); e != nil {
			return e
		}
		return deps.probe(filepath.Join(dir, hex.EncodeToString(nonce)+".json"))
	}})
}
