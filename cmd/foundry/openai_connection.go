package main

import (
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/keychaincheck"
	"foundry/internal/store"
	"path/filepath"
	"strings"
	"time"
)

// Only package-local tests replace the native runner or clock. There is no
// settings, environment, HTTP, or imported-report path to credential readiness.
type openaiConnectionDeps struct {
	probe        func(string) error
	desktopProbe func(string) error
	now          func() time.Time
}

func nativeOpenAIConnectionDeps() openaiConnectionDeps {
	return openaiConnectionDeps{probe: keychaincheck.Probe, desktopProbe: keychaincheck.ProbeDesktop, now: time.Now}
}

func validateOpenAIStartupOptions(cfg config) error {
	if cfg.openaiInferenceRoute != "" {
		if cfg.openaiSubscriptionRoute != "" || cfg.openaiKeychainJournal != "" || cfg.openaiCatalogRoute != "" {
			return errors.New("--openai-inference-route is mutually exclusive with connection, catalog and native probe startup options")
		}
		if domain.ValidateModelCredentialOperation(cfg.openaiInferenceRoute) != nil {
			return errors.New("invalid OpenAI inference route identifier")
		}
	}
	if cfg.openaiCatalogRoute != "" {
		if cfg.openaiSubscriptionRoute != "" || cfg.openaiKeychainJournal != "" {
			return errors.New("--openai-catalog-route is mutually exclusive with connection startup options")
		}
		if domain.ValidateModelCredentialOperation(cfg.openaiCatalogRoute) != nil {
			return errors.New("invalid OpenAI catalog route identifier")
		}
	}
	if (cfg.openaiSubscriptionRoute == "") != (cfg.openaiKeychainJournal == "") {
		return errors.New("--openai-subscription-route and --openai-keychain-journal must be supplied together")
	}
	if cfg.openaiSubscriptionRoute == "" {
		return nil
	}
	if domain.ValidateModelCredentialOperation(cfg.openaiSubscriptionRoute) != nil {
		return errors.New("invalid OpenAI subscription route identifier")
	}
	if strings.ContainsRune(cfg.openaiKeychainJournal, 0) || !filepath.IsAbs(cfg.openaiKeychainJournal) || filepath.Clean(cfg.openaiKeychainJournal) != cfg.openaiKeychainJournal {
		return errors.New("OpenAI Keychain journal path must be clean and absolute")
	}
	return nil
}

func activateOpenAIConnection(cfg config, ledger *store.Store, runtime *productionModels, recoveryReadOnly bool, deps openaiConnectionDeps) error {
	if err := validateOpenAIStartupOptions(cfg); err != nil {
		return err
	}
	if cfg.openaiSubscriptionRoute == "" {
		return nil
	}
	if recoveryReadOnly {
		return errors.New("OpenAI activation is unavailable in diagnostic read-only recovery")
	}
	route, err := ledger.GetModelRoute(cfg.openaiSubscriptionRoute)
	if err != nil {
		return errors.New("OpenAI subscription route is unavailable")
	}
	if deps.probe == nil || deps.now == nil {
		return errors.New("OpenAI activation dependencies are unavailable")
	}
	// Validate the exact stored route, enabled account and official SIWC
	// instance before native effects. Publish only after a successful diagnostic;
	// recovery remains mandatory before proposals. Reads never refresh the lease.
	var expires time.Time
	controller := acceptance.NewProductionConnection(ledger, acceptance.ProductionConnectionOptions{
		Route: route, SIWC: runtime.siwc,
		CredentialReady: func() bool { return !expires.IsZero() && deps.now().Before(expires) },
	})
	if controller.ProductionConnectionStatus(route.AccountID).Admission == nil {
		_ = controller.Close()
		return errors.New("OpenAI subscription route or account is ineligible")
	}
	if err := deps.probe(cfg.openaiKeychainJournal); err != nil {
		_ = controller.Close()
		return errors.New("OpenAI Keychain diagnostic did not complete successfully; retain its journal for exact-item cleanup")
	}
	expires = deps.now().Add(10 * time.Minute)
	runtime.acceptance = controller
	return nil
}
