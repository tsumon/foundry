package main

import (
	"context"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/store"
	"runtime"
)

// Composition only. No credential readiness probe, recovery deletion, provider
// discovery, Docker command or automatic sign-in is performed here.
func newCodingRuntime(s *store.Store, dataDir string, secrets model.Secrets) (productionModels, *engine.Engine, error) {
	p := newProductionModels(s, nil, secrets)
	p.codingMode = true
	var e *engine.Engine
	p.router = model.NewRouter(nil, p.accounts, model.RouterOptions{CodingAdmission: func(ctx context.Context, q model.Request) error {
		if e == nil {
			return engine.ErrC4Unavailable
		}
		return e.AdmitCodingModelCall(ctx, q)
	}})
	cap, err := engine.NewCodingDockerCapability(p.router)
	if err != nil && runtime.GOOS != "windows" {
		p.siwc.Close()
		p.acceptance.Close()
		return p, nil, err
	}
	e = engine.New(s, p.router, engine.Options{DataDir: dataDir, C4Controlled: cap, CodingWorkbench: true, DisableControlledReadCreation: true})
	return p, e, nil
}
