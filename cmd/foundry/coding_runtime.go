package main

import (
	"context"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/store"
	"foundry/internal/vmruntime"
	"os"
	"path/filepath"
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
	// Missing/untrusted VM assets stay visibly unavailable. Docker remains a
	// distinct explicitly selected new-task capability, never a run fallback.
	if bundle := os.Getenv("FOUNDRY_VM_BUNDLE"); bundle != "" {
		instanceRoot := filepath.Join(dataDir, "vm-instances")
		if root, resolveErr := filepath.EvalSymlinks(dataDir); resolveErr == nil {
			instanceRoot = filepath.Join(root, "vm-instances")
		}
		if mkdirErr := os.MkdirAll(instanceRoot, 0700); mkdirErr == nil {
			if vm, loadErr := vmruntime.Load(bundle, instanceRoot); loadErr == nil {
				cap, err = engine.NewCodingVMCapability(p.router, vm)
			}
		}
	}
	if err != nil && runtime.GOOS != "windows" {
		p.siwc.Close()
		p.acceptance.Close()
		return p, nil, err
	}
	e = engine.New(s, p.router, engine.Options{DataDir: dataDir, C4Controlled: cap, CodingWorkbench: true, DisableControlledReadCreation: true})
	return p, e, nil
}
