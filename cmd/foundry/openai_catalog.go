package main

import (
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/store"
)

func activateOpenAICatalog(cfg config, ledger *store.Store, runtime *productionModels, recoveryReadOnly bool) error {
	if e := validateOpenAIStartupOptions(cfg); e != nil {
		return e
	}
	if cfg.openaiCatalogRoute == "" {
		return nil
	}
	if recoveryReadOnly {
		return errors.New("OpenAI catalog activation is unavailable in diagnostic read-only recovery")
	}
	route, e := ledger.GetModelRoute(cfg.openaiCatalogRoute)
	if e != nil {
		return errors.New("OpenAI catalog route is unavailable")
	}
	controller := acceptance.NewProductionCatalog(ledger, acceptance.ProductionCatalogOptions{Route: route, Resolver: runtime.accounts})
	if controller.ProductionCatalogStatus(route.AccountID).Admission == nil {
		_ = controller.Close()
		return errors.New("OpenAI catalog route or account is ineligible")
	}
	runtime.acceptance = controller
	runtime.catalogMode = true
	return nil
}
