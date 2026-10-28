package main

import (
	"context"
	"errors"
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
	var currentV1 *vmruntime.Runtime
	// Missing/untrusted VM assets stay visibly unavailable. Docker remains a
	// distinct explicitly selected new-task capability, never a run fallback.
	if bundle := os.Getenv("FOUNDRY_VM_BUNDLE"); bundle != "" {
		instanceRoot := filepath.Join(dataDir, "vm-instances")
		if root, resolveErr := filepath.EvalSymlinks(dataDir); resolveErr == nil {
			instanceRoot = filepath.Join(root, "vm-instances")
		}
		if mkdirErr := os.MkdirAll(instanceRoot, 0700); mkdirErr == nil {
			if vm, loadErr := vmruntime.Load(bundle, instanceRoot); loadErr == nil {
				archive := filepath.Join(filepath.Dir(instanceRoot), "vm-bundles")
				retainedPath, retainErr := vmruntime.Retain(bundle, archive, vm.Identity())
				if retainErr != nil {
					p.siwc.Close()
					p.acceptance.Close()
					return p, nil, errors.New("verified runtime retention failed; no fallback permitted")
				}
				current, loadErr := vmruntime.Load(retainedPath, instanceRoot)
				retained, archiveErr := vmruntime.LoadRetained(archive, instanceRoot)
				if loadErr != nil || archiveErr != nil {
					p.siwc.Close()
					p.acceptance.Close()
					return p, nil, errors.New("retained runtime integrity check failed; no fallback permitted")
				}
				currentV1 = current
				cap, err = engine.NewCodingVMCapability(p.router, current, retained...)
			}
		}
	}

	// Optional v2 is a distinct installed resource. Failure to load it never
	// substitutes its metadata for v1 or disables an otherwise valid v1 runtime.
	instanceRoot := filepath.Join(dataDir, "vm-instances")
	if root, resolveErr := filepath.EvalSymlinks(dataDir); resolveErr == nil {
		instanceRoot = filepath.Join(root, "vm-instances")
	}
	archive := filepath.Join(filepath.Dir(instanceRoot), "vm-bundles")
	var currentV2 *vmruntime.Runtime
	if bundle := os.Getenv("FOUNDRY_VM_V2_BUNDLE"); bundle != "" {
		if mkdirErr := os.MkdirAll(instanceRoot, 0700); mkdirErr == nil {
			if installed, loadErr := vmruntime.LoadV2(bundle, instanceRoot); loadErr == nil {
				retainedPath, retainErr := vmruntime.Retain(bundle, archive, installed.Identity())
				if retainErr != nil {
					p.siwc.Close()
					p.acceptance.Close()
					return p, nil, errors.New("verified v2 runtime retention failed")
				}
				currentV2, loadErr = vmruntime.LoadV2(retainedPath, instanceRoot)
				if loadErr != nil {
					p.siwc.Close()
					p.acceptance.Close()
					return p, nil, errors.New("retained v2 runtime integrity check failed")
				}
			}
		}
	}
	if currentV2 != nil || currentV1 == nil {
		if info, statErr := os.Stat(archive); statErr == nil && info.IsDir() {
			retained, archiveErr := vmruntime.LoadRetained(archive, instanceRoot)
			if archiveErr != nil {
				p.siwc.Close()
				p.acceptance.Close()
				return p, nil, errors.New("retained runtime integrity check failed")
			}
			cap, err = engine.NewCodingVMCapabilityWithV2(p.router, currentV1, currentV2, retained...)
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
