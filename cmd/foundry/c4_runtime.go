package main

import (
	"context"
	"errors"
	"foundry/internal/c4fixture"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/httpapi"
	"foundry/internal/store"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type c4RuntimeFactory func(engine.C4Activation, *c4fixture.Gateway) (*engine.C4ControlledCapability, error)

func newC4ControlledRuntime(s *store.Store, dataDir string, c engine.C4Activation, factory c4RuntimeFactory) (*engine.Engine, bool, error) {
	cap, err := factory(c, c4fixture.New(s))
	if err != nil {
		return nil, false, errC4Config
	}
	controller := engine.New(s, nil, engine.Options{DataDir: dataDir, C4Controlled: cap, DisableControlledReadCreation: true})
	readOnly := false
	if err = controller.CheckC4ControlledRecovery(); err != nil {
		if !errors.Is(err, engine.ErrC4Unavailable) {
			controller.Close()
			return nil, false, err
		}
		readOnly = true
	}
	profiles, err := s.ListProfiles()
	if err != nil {
		controller.Close()
		return nil, false, err
	}
	for _, p := range profiles {
		if p.ID != "synthetic-c4" || p.Kind != "synthetic" || p.Protocol != "chat_completions" || p.BaseURL != "https://synthetic.invalid" || p.Model != "C4-LOCAL-FIXTURE-NOT-CLOUD" || p.HasKey || p.AccountID != "" || p.RouteID != "" {
			readOnly = true
		}
	}
	accounts, err := s.ListModelAccounts()
	if err != nil {
		controller.Close()
		return nil, false, err
	}
	if len(accounts) != 0 {
		readOnly = true
	}
	routes, err := s.ListModelRoutes()
	if err != nil {
		controller.Close()
		return nil, false, err
	}
	if len(routes) != 0 {
		readOnly = true
	}
	if !readOnly {
		if len(profiles) == 0 {
			if err = s.PutProfile(domain.Profile{ID: "synthetic-c4", Name: "SYNTHETIC C4 accountless fixture", Kind: "synthetic", Protocol: "chat_completions", BaseURL: "https://synthetic.invalid", Model: "C4-LOCAL-FIXTURE-NOT-CLOUD"}); err != nil {
				controller.Close()
				return nil, false, err
			}
			if err = s.PutSettings(domain.Settings{DefaultProfileID: "synthetic-c4"}); err != nil {
				controller.Close()
				return nil, false, err
			}
		}
		if err = controller.Recover(context.Background()); err != nil {
			controller.Close()
			return nil, false, errors.New("C4 local recovery failed")
		}
	}
	return controller, readOnly, nil
}

// This branch has no account, Keychain, provider, MCP or legacy runner service.
// It is reached only after explicit strict configuration, before ordinary setup.
func runC4Controlled(cfg config, s *store.Store, c engine.C4Activation) error {
	controller, readOnly, err := newC4ControlledRuntime(s, cfg.dataDir, c, engine.NewC4DockerCapability)
	if err != nil {
		return err
	}
	defer controller.Close()
	handler := httpapi.New(s, controller, nil, nil, httpapi.Options{Address: cfg.address, WebDir: cfg.webDir, C4ControlledOnly: true, RecoveryReadOnly: readOnly, RecoveryError: "c4_unavailable"})
	listener, err := net.Listen("tcp", cfg.address)
	if err != nil {
		return errors.New("C4 local listener unavailable")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Print("FOUNDRY isolated C4 controller listening; readiness and execution require separate approvals")
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("C4 local listener stopped")
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			server.Close()
			return errors.New("C4 local shutdown failed")
		}
		return nil
	}
}
