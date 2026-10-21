package main

import (
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/store"
)

func activateOpenAIInference(cfg config, ledger *store.Store, runtime *productionModels, recoveryReadOnly bool) error {
	if e := validateOpenAIStartupOptions(cfg); e != nil {
		return e
	}
	if cfg.openaiInferenceRoute == "" {
		return nil
	}
	if recoveryReadOnly {
		return errors.New("OpenAI inference activation is unavailable in diagnostic read-only recovery")
	}
	route, e := ledger.GetModelRoute(cfg.openaiInferenceRoute)
	if e != nil {
		return errors.New("OpenAI inference route is unavailable")
	}
	controller := acceptance.NewProductionInference(ledger, acceptance.ProductionInferenceOptions{Route: route, Resolver: runtime.accounts})
	if controller.ProductionInferenceStatus(route.AccountID).Admission == nil {
		_ = controller.Close()
		return errors.New("OpenAI inference route or account is ineligible")
	}
	runtime.acceptance = controller
	runtime.inferenceMode = true
	return nil
}
