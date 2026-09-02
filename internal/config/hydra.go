package config

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func EnsureHydraMigrated(hydraBin string, configPath string, dataDir string) error {
	markerFile := filepath.Join(dataDir, ".hydra_migrated")

	if _, err := os.Stat(markerFile); err == nil {
		return nil
	}

	log.Println("[hydra] First startup detected: Running SQL migrations...")

	// hydra -c /path/to/hydra.yml migrate sql up --yes
	cmd := exec.Command(hydraBin, "-c", configPath, "migrate", "sql", "up", "--yes")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hydra sql migration failed: %w", err)
	}

	if err := os.WriteFile(markerFile, []byte("migrated"), 0644); err != nil {
		log.Printf("[hydra] Warning: failed to write migration marker file: %v", err)
	}

	log.Println("[hydra] SQL migrations successfully applied.")
	return nil
}

func ConfigureHydraClients(hydraBin string, cfg *Config) error {
	markerFile := filepath.Join(cfg.DataDir, ".hydra_configured")

	if _, err := os.Stat(markerFile); err == nil {
		return nil
	}

	log.Println("[hydra] First startup detected: Waiting for Hydra Admin API to be ready...")

	adminEndpoint := "http://127.0.0.1:4445"
	readyURL := fmt.Sprintf("%s/health/ready", adminEndpoint)

	httpClient := http.Client{Timeout: 2 * time.Second}
	ready := false
	for i := 0; i < 30; i++ {
		resp, err := httpClient.Get(readyURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(1 * time.Second)
	}

	if !ready {
		return fmt.Errorf("hydra admin API was not ready within timeout")
	}

	log.Println("[hydra] Admin API is up. Configuring OAuth2 clients...")

	webmailRedirects := fmt.Sprintf(
		"https://web.%s/auth/callback,https://web.%s/en/auth/callback,https://web.%s/fr/auth/callback",
		cfg.Domain, cfg.Domain, cfg.Domain,
	)

	cmdWebmail := exec.Command(hydraBin, "create", "oauth2-client",
		"--endpoint", adminEndpoint,
		"--id", "stalwart",
		"--name", "AurionMail Webmail",
		"--secret", cfg.SecretBulwarkSSO,
		"--access-token-strategy", "jwt",
		"--audience", "stalwart",
		"--grant-type", "authorization_code,refresh_token",
		"--response-type", "code",
		"--scope", "openid,profile,email,offline_access",
		"--redirect-uri", webmailRedirects,
		"--token-endpoint-auth-method", "client_secret_post",
		"--skip-consent",
	)
	cmdWebmail.Stdout = os.Stdout
	cmdWebmail.Stderr = os.Stderr

	if err := cmdWebmail.Run(); err != nil {
		return fmt.Errorf("failed to create stalwart OAuth2 client: %w", err)
	}

	cryptpadRedirect := fmt.Sprintf("https://pad.%s/ssoauth", cfg.Domain)

	cmdCryptpad := exec.Command(hydraBin, "create", "oauth2-client",
		"--endpoint", adminEndpoint,
		"--id", "cryptpad",
		"--name", "CryptPad",
		"--secret", cfg.SecretCryptpadSSO,
		"--access-token-strategy", "jwt",
		"--grant-type", "authorization_code,refresh_token",
		"--response-type", "code",
		"--scope", "openid,profile,email,offline_access",
		"--redirect-uri", cryptpadRedirect,
		"--token-endpoint-auth-method", "client_secret_basic",
		"--skip-consent",
	)
	cmdCryptpad.Stdout = os.Stdout
	cmdCryptpad.Stderr = os.Stderr

	if err := cmdCryptpad.Run(); err != nil {
		return fmt.Errorf("failed to create cryptpad OAuth2 client: %w", err)
	}

	if err := os.WriteFile(markerFile, []byte("configured"), 0644); err != nil {
		log.Printf("[hydra] Warning: failed to write configuration marker file: %v", err)
	}

	log.Println("[hydra] OAuth2 clients configured successfully.")
	return nil
}
