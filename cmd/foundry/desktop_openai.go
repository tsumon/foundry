package main

import (
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/store"
)

// Only the trusted desktop coding composition calls this constructor. The
// platform and dependencies are private test seams, never flags or settings.
func newDesktopOpenAI(s *store.Store, p *productionModels, dataDir, platform string, desktop, coding, readonly bool, deps openaiConnectionDeps) *acceptance.DesktopCoordinator {
	if (platform != "darwin" && platform != "windows") || !desktop || !coding || readonly {
		return nil
	}
	return acceptance.NewDesktopCoordinator(s, acceptance.DesktopOptions{SIWC: p.siwc, Resolver: p.accounts, Now: deps.now, Probe: func() error {
		if deps.desktopProbe == nil {
			return errors.New("diagnostic unavailable")
		}
		return deps.desktopProbe(dataDir)
	}})
}
