package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config represents the parsed central environment configuration.
type Config struct {
	Domain      string
	DataDir     string
	Port        string // Orchestrator proxy port (defaults to 8080)
	DatabaseURL string

	// Raw key-value store loaded from .env
	Raw map[string]string
}

// Load reads and parses a .env file from the given path.
func Load(envPath string) (*Config, error) {
	file, err := os.Open(envPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open configuration file %s: %w", envPath, err)
	}
	defer file.Close()

	raw := make(map[string]string)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split by first '='
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Strip optional quotes
		val = strings.Trim(val, `"'`)
		raw[key] = val
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading configuration file: %w", err)
	}

	cfg := &Config{
		Domain:  raw["DOMAIN"],
		DataDir: raw["DATA_DIR"],
		Port:    raw["ORCHESTRATOR_PORT"],
		Raw:     raw,
	}

	// Set sensible defaults
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}

	// Construct Database URL if individual PG vars exist
	if pgHost, ok := raw["POSTGRES_HOST"]; ok {
		pgPort := raw["POSTGRES_PORT"]
		if pgPort == "" {
			pgPort = "5432"
		}
		pgUser := raw["POSTGRES_USER"]
		pgPass := raw["POSTGRES_PASSWORD"]
		pgSSL := raw["POSTGRES_SSLMODE"]
		if pgSSL == "" {
			pgSSL = "disable"
		}

		cfg.DatabaseURL = fmt.Sprintf("postgres://%s:%s@%s:%s/aurion?sslmode=%s",
			pgUser, pgPass, pgHost, pgPort, pgSSL)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that mandatory central settings are present.
func (c *Config) Validate() error {
	if c.Domain == "" {
		return fmt.Errorf("missing mandatory configuration: DOMAIN")
	}
	if c.DataDir == "" {
		return fmt.Errorf("missing mandatory configuration: DATA_DIR")
	}
	return nil
}

// GetHydraEnv builds the environment variables required for Ory Hydra.
func (c *Config) GetHydraEnv() map[string]string {
	dbURL := c.Raw["HYDRA_DATABASE_URL"]
	if dbURL == "" && c.DatabaseURL != "" {
		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/hydra?sslmode=%s",
			c.Raw["POSTGRES_USER"], c.Raw["POSTGRES_PASSWORD"],
			c.Raw["POSTGRES_HOST"], getOrDefault(c.Raw, "POSTGRES_PORT", "5432"),
			getOrDefault(c.Raw, "POSTGRES_SSLMODE", "disable"))
	}

	return map[string]string{
		"SERVE_PUBLIC_PORT": "4444",
		"SERVE_ADMIN_PORT":  "4445",
		"URLS_SELF_ISSUER":  fmt.Sprintf("https://oauth.%s/", c.Domain),
		"DSN":               dbURL,
		"SECRETS_SYSTEM":    c.Raw["HYDRA_SYSTEM_SECRET"],
	}
}

// GetSSOEnv builds environment variables for the SSO Node service.
func (c *Config) GetSSOEnv() map[string]string {
	return map[string]string{
		"PORT":         "3030",
		"NODE_ENV":     "production",
		"DOMAIN":       c.Domain,
		"DATA_DIR":     filepath.Join(c.DataDir, "storage", "sso"),
		"DATABASE_URL": c.Raw["SSO_DATABASE_URL"],
		"HYDRA_ADMIN":  "http://127.0.0.1:4445",
	}
}

// GetCryptpadEnv builds environment variables for CryptPad.
func (c *Config) GetCryptpadEnv() map[string]string {
	return map[string]string{
		"PORT":               "3010",
		"WEBSOCKET_PORT":     "3013",
		"USE_CUSTOM_STORAGE": "true",
		"CRYPTPAD_DATA_DIR":  filepath.Join(c.DataDir, "storage", "cryptpad"),
		"MAIN_DOMAIN":        fmt.Sprintf("https://pad.%s", c.Domain),
		"SANDBOX_DOMAIN":     fmt.Sprintf("https://sand.%s", c.Domain),
	}
}

// GetCoreAPIEnv builds environment variables for the Go Core API.
func (c *Config) GetCoreAPIEnv() map[string]string {
	return map[string]string{
		"PORT":         "8070",
		"DOMAIN":       c.Domain,
		"DATA_DIR":     filepath.Join(c.DataDir, "storage", "core"),
		"DATABASE_URL": c.Raw["CORE_API_DATABASE_URL"],
	}
}

// GetWebmailEnv builds environment variables for BulwarkMail / Web App.
func (c *Config) GetWebmailEnv() map[string]string {
	return map[string]string{
		"PORT":         "3000",
		"NODE_ENV":     "production",
		"DOMAIN":       c.Domain,
		"UPLOADS_PATH": filepath.Join(c.DataDir, "storage", "bulwark"),
		"API_URL":      fmt.Sprintf("https://api.%s", c.Domain),
	}
}

func getOrDefault(m map[string]string, key, fallback string) string {
	if val, ok := m[key]; ok && val != "" {
		return val
	}
	return fallback
}
