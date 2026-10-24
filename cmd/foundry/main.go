package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/desktophost"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/hooks"
	"foundry/internal/httpapi"
	"foundry/internal/mcptest"
	"foundry/internal/model"
	"foundry/internal/platformfs"
	"foundry/internal/store"
)

type config struct {
	codingWorkbench               bool
	c4ControlConfig               string
	localMCPTest                  bool
	disableControlledReadCreation bool
	dataDir                       string
	address                       string
	webDir                        string
	dockerPath                    string
	openaiSubscriptionRoute       string
	openaiKeychainJournal         string
	openaiCatalogRoute            string
	openaiInferenceRoute          string
}

type dataLock struct {
	file *platformfs.File
}

type productionModels struct {
	codingMode    bool
	accounts      *accounts.Service
	router        *model.Router
	siwc          *accounts.SIWCService
	acceptance    *acceptance.Service
	catalogMode   bool
	inferenceMode bool
}

func newProductionModels(s *store.Store, legacy *model.Client, secrets model.Secrets) productionModels {
	accountService := accounts.New(s, secrets)
	return productionModels{
		accounts: accountService,
		router:   model.NewRouter(legacy, accountService, model.RouterOptions{}),
		siwc:     accounts.NewSIWCService(accountService),
		// Deliberately no production policy or native-readiness callback.
		acceptance: acceptance.New(s),
	}
}

func (p productionModels) Recover(ctx context.Context) error {
	// Catalog/inference startup is metadata-only, including abandoned credential
	// cleanup exists. Such intents remain blockers, never native deletions.
	if !p.catalogMode && !p.inferenceMode && !p.codingMode {
		if err := p.accounts.Recover(ctx); err != nil {
			return errors.New("recover model accounts failed")
		}
	}
	if err := p.acceptance.Recover(); err != nil {
		return errors.New("recover model acceptance failed")
	}
	return nil
}

func main() {
	if err := runWithArgs(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// Existing subprocess helpers intentionally have no application arguments.
	return runWithArgs(nil)
}

func runWithArgs(args []string) error {
	return runWithSecrets(args, nil)
}

func runWithSecrets(args []string, secrets model.Secrets) error {
	if handled, err := retainDesktopRuntime(args); handled {
		return err
	}
	if handled, err := prepareDesktopData(args); handled {
		return err
	}
	desktop, err := desktopOptions(args)
	if err != nil {
		return err
	}
	cfg, err := loadConfigMode(desktop)
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("foundry", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&desktop, "desktop", false, "private desktop parent protocol")
	flags.BoolVar(&cfg.codingWorkbench, "coding-workbench", false, "explicit controlled desktop coding workbench")
	flags.StringVar(&cfg.c4ControlConfig, "c4-control-config", "", "explicit isolated C4 activation configuration file")
	flags.BoolVar(&cfg.localMCPTest, "local-mcp-test", false, "enable the controller-owned synthetic local MCP reference service")
	flags.StringVar(&cfg.openaiSubscriptionRoute, "openai-subscription-route", "", "explicitly enable one existing OpenAI subscription connection route")
	flags.StringVar(&cfg.openaiKeychainJournal, "openai-keychain-journal", "", "new absolute private journal for the native startup probe")
	flags.StringVar(&cfg.openaiCatalogRoute, "openai-catalog-route", "", "enable independent single-request catalog approvals for one existing OpenAI subscription route")
	flags.StringVar(&cfg.openaiInferenceRoute, "openai-inference-route", "", "enable independent fixed single-request inference approvals for one existing OpenAI subscription route")
	if err = flags.Parse(args); err != nil {
		return errors.New("invalid startup options; supported options: --local-mcp-test, --openai-subscription-route, --openai-keychain-journal, --openai-catalog-route, --openai-inference-route")
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected startup arguments")
	}
	var desktopSession *desktophost.Session
	if desktop {
		desktopSession, err = desktophost.Open(context.Background(), os.Stdin)
		if err != nil {
			return err
		}
		cfg.dockerPath = "/__foundry_desktop_docker_disabled__"
		if !cfg.codingWorkbench {
			secrets = desktopSecrets{}
		}
	}
	var c4Activation *engine.C4Activation
	if cfg.c4ControlConfig != "" {
		if cfg.localMCPTest || cfg.openaiSubscriptionRoute != "" || cfg.openaiKeychainJournal != "" || cfg.openaiCatalogRoute != "" || cfg.openaiInferenceRoute != "" {
			return errC4Config
		}
		c, err := readC4Config(cfg.c4ControlConfig, time.Now())
		if err != nil {
			return err
		}
		c4Activation = &c
	}
	if err := validateOpenAIStartupOptions(cfg); err != nil {
		return err
	}
	if err := platformfs.PrivateDirectory(cfg.dataDir); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	lock, err := acquireDataLock(cfg.dataDir)
	if err != nil {
		return err
	}
	defer lock.Close()

	persistent, err := store.Open(filepath.Join(cfg.dataDir, "foundry.db"))
	if err != nil {
		return err
	}
	defer persistent.Close()
	if c4Activation != nil {
		return runC4Controlled(cfg, persistent, *c4Activation)
	}
	// Decide instance-wide readiness before any recovery, observer or owned
	// MCP service can mutate history. Do not swallow other startup failures.
	recoveryReadOnly := false
	recoveryError := "invalid_mission_model_snapshot"
	checkRecovery := engine.CheckRecoveryModelSnapshots
	if cfg.codingWorkbench {
		checkRecovery = engine.CheckCodingRecoveryModelSnapshots
	}
	if err := checkRecovery(persistent); err != nil {
		if !errors.Is(err, domain.ErrInvalidModelSnapshot) && !errors.Is(err, engine.ErrC4Unavailable) {
			return fmt.Errorf("check recovery structure: %w", err)
		}
		if errors.Is(err, engine.ErrC4Unavailable) {
			recoveryError = "c4_unavailable"
		}
		recoveryReadOnly = true
	}
	if !recoveryReadOnly {
		if err := persistent.RecoverDiscoveries(); err != nil {
			return errors.New("recover capability discoveries failed")
		}
	}
	var descriptor *mcptest.Descriptor
	if cfg.localMCPTest && !recoveryReadOnly {
		service, err := mcptest.Start(context.Background(), persistent)
		if err != nil {
			return fmt.Errorf("start owned local MCP test service: %w", err)
		}
		defer service.Close()
		d := service.Descriptor()
		descriptor = &d
	}

	if secrets == nil {
		secrets = model.NewSecrets()
	}
	client := model.NewClient(secrets)
	models := newProductionModels(persistent, client, secrets)
	var controller *engine.Engine
	if cfg.codingWorkbench {
		models.siwc.Close()
		models.acceptance.Close()
		models, controller, err = newCodingRuntime(persistent, cfg.dataDir, secrets)
		if err != nil {
			return err
		}
		if !controller.CodingWorkbenchAvailable() {
			recoveryReadOnly = true
			recoveryError = "coding_workbench_unavailable"
		}
		if !recoveryReadOnly {
			if err = controller.CheckC4ControlledRecovery(); err != nil {
				if !errors.Is(err, engine.ErrC4Unavailable) {
					controller.Close()
					models.siwc.Close()
					models.acceptance.Close()
					return err
				}
				recoveryReadOnly = true
				recoveryError = "c4_unavailable"
				if errors.Is(err, engine.ErrC4RuntimeMismatch) {
					recoveryError = "c4_runtime_mismatch"
				}
			}
		}
	}
	defer models.siwc.Close()
	if err := activateOpenAIConnection(cfg, persistent, &models, recoveryReadOnly, nativeOpenAIConnectionDeps()); err != nil {
		return err
	}
	if err := activateOpenAICatalog(cfg, persistent, &models, recoveryReadOnly); err != nil {
		return err
	}
	if err := activateOpenAIInference(cfg, persistent, &models, recoveryReadOnly); err != nil {
		return err
	}
	defer models.acceptance.Close()
	if !recoveryReadOnly {
		if err := models.Recover(context.Background()); err != nil {
			return err
		}
	}
	if controller == nil {
		controller = engine.New(persistent, models.router, engine.Options{DataDir: cfg.dataDir, DockerPath: cfg.dockerPath, DisableControlledReadCreation: cfg.disableControlledReadCreation, LocalMCPTest: descriptor})
	}
	defer controller.Close()
	if !recoveryReadOnly {
		if err := controller.Recover(context.Background()); err != nil {
			return fmt.Errorf("recover controller: %w", err)
		}
	}
	observer := hooks.New(persistent)
	if !recoveryReadOnly && !cfg.codingWorkbench {
		observer.Start()
	}
	defer observer.Close()

	listener, err := net.Listen("tcp", cfg.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.address, err)
	}
	defer listener.Close()
	cfg.address = listener.Addr().String()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if desktopSession != nil {
		go func() {
			select {
			case <-desktopSession.Context.Done():
				stop()
			case <-ctx.Done():
			}
		}()
	}
	var desktopAccounts *acceptance.DesktopCoordinator
	if cfg.openaiSubscriptionRoute == "" && cfg.openaiCatalogRoute == "" && cfg.openaiInferenceRoute == "" {
		desktopAccounts = newDesktopOpenAI(persistent, &models, cfg.dataDir, runtime.GOOS, desktopSession != nil, cfg.codingWorkbench, recoveryReadOnly, nativeOpenAIConnectionDeps())
	}
	if desktopAccounts != nil {
		defer desktopAccounts.Close()
	}
	handler := httpapi.New(persistent, controller, client, secrets, httpapi.Options{DesktopAccounts: desktopAccounts, CodingWorkbench: cfg.codingWorkbench, Address: cfg.address, WebDir: cfg.webDir, Hooks: observer, Accounts: models.accounts, ModelRouter: models.router, SIWC: models.siwc, Acceptance: models.acceptance, RecoveryReadOnly: recoveryReadOnly, RecoveryError: recoveryError})
	if desktopSession != nil {
		handler = desktophost.Protect(desktopSession.Capability, handler)
	}
	server := &http.Server{
		Addr: cfg.address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	if desktopSession != nil {
		if err := desktophost.Ready(os.Stdout, "http://"+cfg.address, cfg.dataDir); err != nil {
			_ = server.Close()
			return err
		}
	}
	log.Printf("FOUNDRY 0.1.0 listening at http://%s", cfg.address)
	if recoveryReadOnly {
		log.Printf("recovery_limited: diagnostic read-only; %s; all business mutations disabled", recoveryError)
	}

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown server: %w", err)
		}
		return nil
	}
}

func loadConfig() (config, error) {
	return loadConfigMode(false)
}

func loadConfigMode(desktop bool) (config, error) {
	var cfg config
	if value := os.Getenv("FOUNDRY_DISABLE_CONTROLLED_READ_CREATION"); value != "" {
		var err error
		cfg.disableControlledReadCreation, err = strconv.ParseBool(value)
		if err != nil {
			return cfg, errors.New("FOUNDRY_DISABLE_CONTROLLED_READ_CREATION must be a boolean")
		}
	}
	dataDir, present := os.LookupEnv("FOUNDRY_DATA_DIR")
	if present && strings.TrimSpace(dataDir) == "" {
		return cfg, errors.New("FOUNDRY_DATA_DIR cannot be empty")
	}
	if !present {
		base, err := os.UserConfigDir()
		if err != nil {
			return cfg, errors.New("determine default data directory")
		}
		dataDir = filepath.Join(base, "foundry")
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return cfg, errors.New("invalid FOUNDRY_DATA_DIR")
	}
	cfg.dataDir = filepath.Clean(absDataDir)

	cfg.address = envOr("FOUNDRY_ADDR", "127.0.0.1:4317")
	host, port, err := net.SplitHostPort(cfg.address)
	if err != nil || host != "127.0.0.1" {
		return cfg, errors.New("FOUNDRY_ADDR must be 127.0.0.1:<port>")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 || (!desktop && portNumber == 0) || (desktop && portNumber != 0) {
		return cfg, errors.New("FOUNDRY_ADDR must contain a valid port")
	}

	if webDir, present := os.LookupEnv("FOUNDRY_WEB_DIR"); present {
		if strings.TrimSpace(webDir) == "" {
			cfg.webDir = ""
		} else {
			cfg.webDir, err = validatedDirectory(webDir)
			if err != nil {
				return cfg, fmt.Errorf("FOUNDRY_WEB_DIR: %w", err)
			}
		}
	} else if info, statErr := os.Stat("web/dist"); statErr == nil && info.IsDir() {
		cfg.webDir, err = filepath.Abs("web/dist")
		if err != nil {
			return cfg, errors.New("invalid default web directory")
		}
	}

	cfg.dockerPath = envOr("FOUNDRY_DOCKER", "docker")
	if strings.TrimSpace(cfg.dockerPath) == "" || strings.ContainsRune(cfg.dockerPath, 0) {
		return cfg, errors.New("FOUNDRY_DOCKER must name the Docker executable")
	}
	return cfg, nil
}

func envOr(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func validatedDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("directory path is invalid")
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", errors.New("directory does not exist")
	}
	return filepath.Clean(absolute), nil
}

func acquireDataLock(directory string) (*dataLock, error) {
	path := filepath.Join(directory, "controller.lock")
	file, err := platformfs.OpenLock(path)
	if err != nil {
		return nil, fmt.Errorf("open controller lock: %w", err)
	}
	if err := lockExclusive(file.File); err != nil {
		_ = file.Close()
		return nil, errors.New("another FOUNDRY controller owns this data directory")
	}
	if err := file.Truncate(0); err != nil {
		_ = unlockExclusive(file.File)
		_ = file.Close()
		return nil, fmt.Errorf("write controller lock: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		_ = unlockExclusive(file.File)
		_ = file.Close()
		return nil, fmt.Errorf("write controller lock: %w", err)
	}
	return &dataLock{file: file}, nil
}

func (l *dataLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockExclusive(l.file.File)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
