// Command foundry-acceptance serves the normal workbench against fresh,
// disposable synthetic data. It is not the production foundry entrypoint.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/gitinspect"
	"foundry/internal/httpapi"
	"foundry/internal/mcptest"
	"foundry/internal/model"
	"foundry/internal/store"
)

type acceptanceRuntime struct {
	browserSIWC                 *accounts.SIWCService
	gates                       *acceptance.Service
	gateProtocol                *protocolFixture
	gateSIWC                    *accounts.SIWCService
	Root, Repo, URL             string
	AccountID, AccountMissionID string
	store                       *store.Store
	engine                      *engine.Engine
	mcp                         *mcptest.Service
	protocol                    *protocolFixture
	router                      *model.Router
	siwc                        *accounts.SIWCService
	scenarios                   map[string]string
	authAccounts                map[string]string
	server                      *http.Server
	cancel                      context.CancelFunc
	serveErr                    chan error
}

func main() {
	webDir := flag.String("web-dir", "web/dist", "built workbench static directory (read-only)")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("only --web-dir is accepted; data and loopback port are always freshly allocated")
	}
	runtime, err := startAcceptance(*webDir)
	if err != nil {
		log.Fatal(err)
	}
	defer runtime.Close()
	// No token, callback, authorization URL or secret is printed. Artifacts are retained after shutdown.
	if err := json.NewEncoder(os.Stdout).Encode(runtime.manifest()); err != nil {
		log.Print(err)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-runtime.serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Print(err)
		}
	}
}

func startAcceptance(webDir string) (_ *acceptanceRuntime, err error) {
	webDir, err = filepath.Abs(webDir)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(filepath.Join(webDir, "index.html")); statErr != nil || !info.Mode().IsRegular() {
		return nil, errors.New("build the frontend first; --web-dir must contain index.html")
	}
	root, err := os.MkdirTemp("", "foundry-r5a-synthetic-")
	if err != nil {
		return nil, err
	}
	r := &acceptanceRuntime{
		Root: root, Repo: filepath.Join(root, "synthetic-repository"), serveErr: make(chan error, 1),
		scenarios: map[string]string{}, authAccounts: map[string]string{},
	}
	defer func() {
		if err != nil {
			r.Close()
			err = fmt.Errorf("synthetic acceptance setup (artifacts retained at %s): %w", root, err)
		}
	}()
	if err = seedRepository(r.Repo); err != nil {
		return nil, err
	}
	r.store, err = store.Open(filepath.Join(root, "foundry.db"))
	if err != nil {
		return nil, err
	}
	if err = r.store.RecoverDiscoveries(); err != nil {
		return nil, err
	}
	secrets := &ephemeralSecrets{values: map[string]string{}}
	accountService := accounts.New(r.store, secrets)
	if err = accountService.Recover(context.Background()); err != nil {
		return nil, err
	}
	r.protocol, err = newProtocolFixture()
	if err != nil {
		return nil, err
	}
	r.siwc, err = accounts.NewSIWCSimulation(accountService, r.protocol.siwcOptions())
	if err != nil {
		return nil, err
	}
	r.router = model.NewRouter(syntheticGateway{}, accountService, model.RouterOptions{SimulationOrigins: []string{r.protocol.modelOrigin()}})
	profile := domain.Profile{ID: "synthetic-stub", Name: "SYNTHETIC local stub model", Protocol: "chat_completions", BaseURL: "http://127.0.0.1:9", Model: "SYNTHETIC-STUB-NOT-CLOUD"}
	if err = r.store.PutProfile(profile); err != nil {
		return nil, err
	}
	if err = r.store.PutSettings(domain.Settings{DefaultProfileID: profile.ID}); err != nil {
		return nil, err
	}
	repository, err := r.store.SaveGitRepository(domain.GitRepository{ID: "synthetic-source", Name: "SYNTHETIC disposable Git source", Path: r.Repo}, 0)
	if err != nil {
		return nil, err
	}
	observation, err := gitinspect.Inspect(context.Background(), r.Repo)
	if err != nil {
		return nil, err
	}
	observation.ID, observation.RepositoryID, observation.RepositoryRevision = "synthetic-observation", repository.ID, repository.Revision
	if _, err = r.store.SaveGitObservation(observation); err != nil {
		return nil, err
	}
	r.mcp, err = mcptest.Start(context.Background(), r.store)
	if err != nil {
		return nil, err
	}
	descriptor := r.mcp.Descriptor()
	r.engine = engine.New(r.store, acceptanceGateway{router: r.router}, engine.Options{DataDir: root, Runner: syntheticRunner{}, LocalMCPTest: &descriptor})
	if err = r.engine.Recover(context.Background()); err != nil {
		return nil, err
	}
	if err = seedModelAccounts(r, accountService); err != nil {
		return nil, err
	}
	r.scenarios["legacy_mcp_approval"] = r.AccountMissionID
	if err = seedProtocolScenarios(r, accountService); err != nil {
		return nil, err
	}
	if err = seedRolePolicyScenarios(r); err != nil {
		return nil, err
	}
	if err = startAcceptanceGates(r, accountService); err != nil {
		return nil, err
	}
	listener, err := listenAcceptance()
	if err != nil {
		return nil, err
	}
	address := listener.Addr().String()
	r.URL = "http://" + address
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	base := httpapi.New(r.store, r.engine, model.NewClient(secrets), secrets, httpapi.Options{Address: address, WebDir: webDir, Accounts: accountService, ModelRouter: r.router, SIWC: r.siwc, Acceptance: r.gates})
	r.server = &http.Server{Handler: acceptanceHandler{runtime: r, base: base, host: address}, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { r.serveErr <- r.server.Serve(listener) }()
	return r, nil
}

// Close stops owned resources, but deliberately retains the temp artifacts.
func (r *acceptanceRuntime) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.server.Shutdown(ctx); err != nil {
			_ = r.server.Close()
		}
		cancel()
	}
	if r.gates != nil {
		_ = r.gates.Close()
	}
	if r.browserSIWC != nil {
		_ = r.browserSIWC.Close()
	}
	if r.siwc != nil {
		_ = r.siwc.Close()
	}
	if r.gateSIWC != nil {
		_ = r.gateSIWC.Close()
	}
	if r.gateProtocol != nil {
		r.gateProtocol.Close()
	}
	if r.engine != nil {
		_ = r.engine.Close()
	}
	if r.mcp != nil {
		_ = r.mcp.Close()
	}
	if r.store != nil {
		_ = r.store.Close()
	}
	if r.protocol != nil {
		r.protocol.Close()
	}
}

func seedRepository(repo string) error {
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("SYNTHETIC source before\n"), 0600); err != nil {
		return err
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "src/main.txt"}, {"-c", "user.name=Synthetic Acceptance", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "SYNTHETIC base"}} {
		command := exec.Command("/usr/bin/git", args...)
		command.Dir = repo
		command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
		if raw, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("seed synthetic Git: %w %s", err, raw)
		}
	}
	return nil
}
