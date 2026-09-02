package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"aurion-orchestrator/internal/assets"
	"aurion-orchestrator/internal/config"
	"aurion-orchestrator/internal/proxy"
	"aurion-orchestrator/internal/runner"
)

func main() {
	envPathFlag := flag.String("env", ".env", "Path to the central .env configuration file")
	flag.Parse()

	log.Println("==================================================")
	log.Println("     Starting Aurion Orchestrator Binary          ")
	log.Println("==================================================")

	// 1. Load Central Configuration
	cfg, err := config.Load(*envPathFlag)
	if err != nil {
		log.Fatalf("[main] Configuration error: %v", err)
	}
	log.Printf("[main] Config loaded. Domain: %s, DataDir: %s, ProxyPort: %s", cfg.Domain, cfg.DataDir, cfg.Port)

	// 2. Initialize Runner / Process Manager
	mngr, err := runner.NewManager(cfg.DataDir)
	if err != nil {
		log.Fatalf("[main] Failed to initialize process manager: %v", err)
	}

	// 3. Unpack Embedded Assets (Node runtime, app bundles, binaries) to DATA_DIR/runtime
	if err := mngr.ExtractEmbeddedFS(assets.RuntimeFS, "assets"); err != nil {
		log.Fatalf("[main] Failed to extract runtime assets: %v", err)
	}

	// 3b. Generate Hydra YAML config
	hydraYamlPath := filepath.Join(mngr.GetRuntimePath(""), "hydra.yml")
	if err := cfg.WriteHydraConfigFile(hydraYamlPath); err != nil {
		log.Fatalf("[main] Failed to write hydra.yml: %v", err)
	}

	hydraBin := mngr.GetRuntimePath("hydra")
	if err := config.EnsureHydraMigrated(hydraBin, hydraYamlPath, cfg.DataDir); err != nil {
		log.Fatalf("[main] Failed to run Hydra migrations: %v", err)
	}

	if err := config.EnsureCoreAPIMigrated(cfg.DataDir, mngr.GetRuntimePath(""), cfg.GetCoreAPIDSN()); err != nil {
		log.Fatalf("[main] Failed to run Core-API migrations: %v", err)
	}

	cryptpadDir := filepath.Join(mngr.GetRuntimePath("apps"), "cryptpad")

	if err := config.EnsureCryptpadExtracted(cryptpadDir); err != nil {
		log.Fatalf("[main] Failed to extract CryptPad zip: %v", err)
	}

	if err := cfg.SetupCryptpadConfigs(cryptpadDir); err != nil {
		log.Fatalf("[main] Failed to setup CryptPad configs: %v", err)
	}

	// 4. Initialize Internal Reverse Proxy
	revProxy, err := proxy.NewProxy(proxy.Config{
		Domain: cfg.Domain,
	}, assets.BridgeTemplatesFS)
	if err != nil {
		log.Fatalf("[main] Failed to initialize reverse proxy: %v", err)
	}

	// 5. Create Root Context with OS Signal Cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 6. Define and Launch Sub-Services
	nodeBin := mngr.GetRuntimePath("node")
	appsDir := mngr.GetRuntimePath("apps")

	services := []runner.ServiceConfig{
		{
			Name:       "hydra",
			Executable: mngr.GetRuntimePath("hydra"),
			Args:       []string{"serve", "all", "-c", hydraYamlPath},
			WorkDir:    mngr.GetRuntimePath(""),
			Env:        map[string]string{},
		},
		{
			Name:       "core-api",
			Executable: mngr.GetRuntimePath("core-api"),
			Args:       []string{},
			WorkDir:    mngr.GetRuntimePath(""),
			Env:        cfg.GetCoreAPIEnv(),
		},
		{
			Name:       "sso",
			Executable: nodeBin,
			Args:       []string{filepath.Join(appsDir, "sso", "lib", "app.js")},
			WorkDir:    filepath.Join(appsDir, "sso"),
			Env:        cfg.GetSSOEnv(),
		},
		{
			Name:       "cryptpad",
			Executable: nodeBin,
			Args:       []string{filepath.Join(appsDir, "cryptpad", "server.js")},
			WorkDir:    cryptpadDir,
			Env:        map[string]string{},
		},
		{
			Name:       "webmail",
			Executable: nodeBin,
			Args:       []string{filepath.Join(appsDir, "webmail", "server.js")},
			WorkDir:    filepath.Join(appsDir, "webmail"),
			Env:        cfg.GetWebmailEnv(),
		},
	}

	log.Println("[main] Starting child services...")
	for _, svc := range services {
		if err := mngr.StartService(ctx, svc); err != nil {
			log.Printf("[main] ERROR: Failed to start service %s: %v", svc.Name, err)
		}
	}

	// 7. Configure HTTP Reverse Proxy Server
	serverAddr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	server := &http.Server{
		Addr:         serverAddr,
		Handler:      revProxy,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// 8. Listen for OS Shutdown Signals (SIGINT, SIGTERM)
	shutdownSig := make(chan os.Signal, 1)
	signal.Notify(shutdownSig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("[main] Reverse Proxy listening on http://%s", serverAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[main] HTTP Proxy crashed: %v", err)
		}
	}()

	// Block until a signal is received
	sig := <-shutdownSig
	log.Printf("[main] Received signal %v. Initiating graceful shutdown...", sig)

	// 9. Graceful Shutdown Sequence
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Stop HTTP Proxy Server
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("[main] Error shutting down HTTP server: %v", err)
	}

	// Stop All Child Sub-Processes
	mngr.StopAll(5 * time.Second)

	log.Println("[main] Aurion Orchestrator stopped cleanly.")
}
