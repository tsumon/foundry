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
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/c4fixture"
	"foundry/internal/desktophost"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/gitinspect"
	"foundry/internal/httpapi"
	"foundry/internal/mcptest"
	"foundry/internal/model"
	"foundry/internal/sandbox"
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
	c4Controls                  *c4ControlsDriver
	repositoryTasks             bool
}

func main() {
	desktop := flag.Bool("desktop", false, "private desktop parent protocol; repository simulation only")
	webDir := flag.String("web-dir", "web/dist", "built workbench static directory (read-only)")
	controls := flag.Bool("c4-controls-fixture", false, "isolated synthetic C4 controls only; never enables Docker")
	repository := flag.Bool("repository-tasks-fixture", false, "isolated descriptor-driven repository simulation; no compiler, Docker or provider execution")
	repositoryPath := flag.String("repository-task-path", "", "optional committed repository with foundry.task.json; read-only HEAD snapshot, ignores uncommitted edits; requires --repository-tasks-fixture")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("only documented flags are accepted; data and loopback port are always freshly allocated")
	}
	if err := validateRepositoryTaskFlags(*controls, *repository, *repositoryPath); err != nil {
		log.Fatal(err)
	}
	var session *desktophost.Session
	if *desktop {
		if *controls || !*repository {
			log.Fatal("desktop acceptance supports repository simulation only")
		}
		var err error
		session, err = desktophost.Open(context.Background(), os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		if !filepath.IsAbs(os.Getenv("FOUNDRY_DATA_DIR")) {
			log.Fatal("desktop requires an absolute retained data directory")
		}
	}
	start := startAcceptance
	if *controls {
		start = startC4ControlsAcceptance
	}
	if *repository {
		start = func(web string) (*acceptanceRuntime, error) {
			if session != nil {
				return startRepositoryTasksDesktop(web, *repositoryPath, os.Getenv("FOUNDRY_DATA_DIR"), session.Capability)
			}
			return startRepositoryTasksAcceptance(web, *repositoryPath)
		}
	}
	runtime, err := start(*webDir)
	if err != nil {
		log.Fatal(err)
	}
	defer runtime.Close()
	// No token, callback, authorization URL or secret is printed. Artifacts are retained after shutdown.
	var readyErr error
	if session != nil {
		readyErr = desktophost.Ready(os.Stdout, runtime.URL, runtime.Root)
	} else {
		readyErr = json.NewEncoder(os.Stdout).Encode(runtime.manifest())
	}
	if readyErr != nil {
		log.Print(readyErr)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if session != nil {
		go func() {
			select {
			case <-session.Context.Done():
				stop()
			case <-ctx.Done():
			}
		}()
	}
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
	baseGateway := acceptanceGateway{router: r.router}
	// Pin an existing canonical C4 root before construction; no runner probe.
	if err = os.MkdirAll(filepath.Join(root, "managed"), 0700); err != nil {
		return nil, err
	}
	r.engine = engine.New(r.store, codingReplanGateway{fallback: codingLoopGateway{fallback: baseGateway}, store: r.store}, engine.Options{DataDir: filepath.Join(root, "managed"), Runner: codingLoopRunner{fallback: syntheticRunner{}}, LocalMCPTest: &descriptor, C4: engine.NewC4SyntheticCapability(sandbox.NewC4SyntheticDriver(&c4FixtureDriver{}), c4fixture.New(r.store), "/synthetic/docker", "unix:///synthetic/docker.sock")})
	if err = r.engine.Recover(context.Background()); err != nil {
		return nil, err
	}
	if err = seedC4Scenarios(r); err != nil {
		return nil, err
	}
	if err = seedCodingLoopScenario(r); err != nil {
		return nil, err
	}
	if err = seedCodingReplanScenario(r); err != nil {
		return nil, err
	}
	if err = seedRepositoryImportScenarios(r); err != nil {
		return nil, err
	}
	if err = seedModelAccounts(r, accountService); err != nil {
		return nil, err
	}
	r.scenarios["legacy_mcp_approval"] = r.AccountMissionID
	if err = seedProtocolScenarios(r, accountService); err != nil {
		return nil, err
	}
	// Historical protocol missions already froze their own selections. New
	// ordinary tasks use the local stub, without account or subscription setup.
	// Restore before proposing policy so its settings digest remains current.
	if err = r.store.PutSettings(allRoleBindings("synthetic-stub", nil)); err != nil {
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
		command, cleanup, err := fixtureGitCommand(args...)
		if err != nil {
			return err
		}
		defer cleanup()
		command.Dir = repo
		if raw, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("seed synthetic Git: %w %s", err, raw)
		}
	}
	return nil
}
