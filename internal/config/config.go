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
	Domain  string
	DataDir string
	Port    string // Orchestrator proxy port (defaults to 8080)

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

	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Domain == "" {
		return fmt.Errorf("missing mandatory configuration: DOMAIN")
	}
	if c.DataDir == "" {
		return fmt.Errorf("missing mandatory configuration: DATA_DIR")
	}
	return nil
}

// -----------------------------------------------------------------------------
// HYDRA CONFIG GENERATOR
// -----------------------------------------------------------------------------

// WriteHydraConfigFile génère le fichier hydra.yml dans le dossier runtime.
func (c *Config) WriteHydraConfigFile(targetPath string) error {
	hydraUser := getOrDefault(c.Raw, "HYDRA_POSTGRES_USER", getOrDefault(c.Raw, "POSTGRES_USER", "hydra"))
	hydraPass := getOrDefault(c.Raw, "HYDRA_POSTGRES_PASSWORD", getOrDefault(c.Raw, "POSTGRES_PASSWORD", "hydra"))
	hydraHost := getOrDefault(c.Raw, "HYDRA_POSTGRES_HOST", "127.0.0.1")
	hydraPort := getOrDefault(c.Raw, "HYDRA_POSTGRES_PORT", "5432")
	hydraSSL := getOrDefault(c.Raw, "HYDRA_POSTGRES_SSLMODE", "disable")
	hydraDB := getOrDefault(c.Raw, "HYDRA_POSTGRES_DB", "hydra")

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&max_conns=20&max_idle_conns=4",
		hydraUser, hydraPass, hydraHost, hydraPort, hydraDB, hydraSSL)

	systemSecret := getOrDefault(c.Raw, "HYDRA_SYSTEM_SECRET", "default_secret_change_me_in_prod")
	pairwiseSalt := getOrDefault(c.Raw, "HYDRA_PAIRWISE_SALT", "default_salt_change_me_in_prod")

	content := fmt.Sprintf(`serve:
  cookies:
    same_site_mode: Lax
dsn: %s
urls:
  self:
    issuer: https://oauth.%s
  consent: https://sso.%s/consent
  login: https://sso.%s/login
  logout: https://sso.%s/logout
  post_logout_redirect: https://sso.%s/exited
  device:
    verification: https://sso.%s/device/verify
    success: https://sso.%s/device/success

secrets:
  system:
    - %s

oidc:
  subject_identifiers:
    supported_types:
      - pairwise
      - public
    pairwise:
      salt: %s
`, dsn, c.Domain, c.Domain, c.Domain, c.Domain, c.Domain, c.Domain, c.Domain, systemSecret, pairwiseSalt)

	return os.WriteFile(targetPath, []byte(content), 0644)
}

// -----------------------------------------------------------------------------
// CRYPTPAD CONFIG GENERATORS
// -----------------------------------------------------------------------------
func (c *Config) SetupCryptpadConfigs(cryptpadRuntimeDir string) error {
	storageDir := filepath.Join(c.DataDir, "storage", "cryptpad")

	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return fmt.Errorf("failed to create cryptpad storage directory: %w", err)
	}

	placeholder := "AURION_DOMAIN_REPLACE_ME"
	targetDirs := []string{"customize", "lib", "www"}

	for _, dir := range targetDirs {
		targetPath := filepath.Join(cryptpadRuntimeDir, dir)

		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			continue
		}

		err := filepath.Walk(targetPath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}

			ext := filepath.Ext(path)
			if ext == ".js" || ext == ".html" {
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}

				if strings.Contains(string(content), placeholder) {
					newContent := strings.ReplaceAll(string(content), placeholder, c.Domain)
					if err := os.WriteFile(path, []byte(newContent), info.Mode()); err != nil {
						return fmt.Errorf("failed to write replaced domain in %s: %w", path, err)
					}
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to replace domain placeholders in %s: %w", dir, err)
		}
	}

	persistentConfigPath := filepath.Join(storageDir, "config.js")

	if _, err := os.Stat(persistentConfigPath); os.IsNotExist(err) {
		defaultConfigJS := fmt.Sprintf(`/* 
 * CryptPad Admin Configuration File
 * Location: %s
 * You can manually edit this file. It will be preserved across restarts.
 */
module.exports = {
    httpUnsafeOrigin: 'https://pad.%s',
    mainDomain: 'https://pad.%s',
    sandboxDomain: 'https://sand.%s',
    httpAddress: '127.0.0.1',
    httpPort: 3010,
    websocketPath: '/cryptpad-websocket',
    storagePath: '%s',
    filePath: '%s',
    archivePath: '%s',
    pinPath: '%s',
    taskPath: '%s',
    blockPath: '%s',
    blobPath: '%s',
    blobStagingPath: '%s',
    decreePath: '%s',
    logPath: '%s',
};
`,
			persistentConfigPath,
			c.Domain, c.Domain, c.Domain,
			filepath.Join(storageDir, "data"),
			filepath.Join(storageDir, "files"),
			filepath.Join(storageDir, "archive"),
			filepath.Join(storageDir, "pins"),
			filepath.Join(storageDir, "tasks"),
			filepath.Join(storageDir, "block"),
			filepath.Join(storageDir, "blob"),
			filepath.Join(storageDir, "blobstage"),
			filepath.Join(storageDir, "decrees"),
			filepath.Join(storageDir, "logs"),
		)

		if err := os.WriteFile(persistentConfigPath, []byte(defaultConfigJS), 0644); err != nil {
			return fmt.Errorf("failed to write initial admin config.js: %w", err)
		}
	}

	configContent, err := os.ReadFile(persistentConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read admin config.js: %w", err)
	}

	runtimeConfigPath := filepath.Join(cryptpadRuntimeDir, "config.js")
	if err := os.WriteFile(runtimeConfigPath, configContent, 0644); err != nil {
		return fmt.Errorf("failed to sync config.js to runtime: %w", err)
	}

	clientID := getOrDefault(c.Raw, "CRYPTPAD_OAUTH_CLIENT_ID", "cryptpad")
	clientSecret := getOrDefault(c.Raw, "CRYPTPAD_OAUTH_CLIENT_SECRET", "SECRET_CRYPTPAD_SSO")

	ssoJS := fmt.Sprintf(`module.exports = {
    enabled: true,
    enforced: true,
    cpPassword: true,
    forceCpPassword: true,
    list: [
        {
            name: 'Aurion SSO',
            type: 'oidc',
            url: 'https://oauth.%s',
            client_id: '%s',
            client_secret: '%s',
            jwt_alg: 'RS256',
            userinfo: false,
            username_claim: 'sub'
        }
    ]
};
`, c.Domain, clientID, clientSecret)

	if err := os.WriteFile(filepath.Join(cryptpadRuntimeDir, "sso.js"), []byte(ssoJS), 0644); err != nil {
		return fmt.Errorf("failed to write sso.js: %w", err)
	}

	return nil
}

// -----------------------------------------------------------------------------
// ENV/ARGS HELPERS FOR RUNNER
// -----------------------------------------------------------------------------

// GetSSOEnv builds environment variables for the SSO Node service.
func (c *Config) GetSSOEnv() map[string]string {
	domainParts := strings.Split(c.Domain, ".")
	dcParts := make([]string, len(domainParts))
	for i, part := range domainParts {
		dcParts[i] = fmt.Sprintf("dc=%s", part)
	}
	defaultDnPattern := fmt.Sprintf("uid={username},ou=people,%s", strings.Join(dcParts, ","))

	return map[string]string{
		"PORT":                     "3030",
		"NODE_ENV":                 "production",
		"DOMAIN":                   c.Domain,
		"DATA_DIR":                 filepath.Join(c.DataDir, "storage", "sso"),
		"BASE_URL":                 fmt.Sprintf("https://sso.%s", c.Domain),
		"HYDRA_ADMIN_URL":          getOrDefault(c.Raw, "HYDRA_ADMIN_URL", "http://127.0.0.1:4445"),
		"ORY_API_KEY":              c.Raw["ORY_API_KEY"],
		"LDAP_URL":                 getOrDefault(c.Raw, "LDAP_URL", "ldap://127.0.0.1:3890"),
		"LDAP_USER_DN_PATTERN":     getOrDefault(c.Raw, "LDAP_USER_DN_PATTERN", defaultDnPattern),
		"WEBMAIL_DOMAIN_WP":        fmt.Sprintf("https://web.%s", c.Domain),
		"CRYPTPAD_DOMAIN_WP":       fmt.Sprintf("https://pad.%s", c.Domain),
		"CORE_API_URL":             fmt.Sprintf("https://api.%s", c.Domain),
		"CORE_API_INTERNAL_SECRET": getOrDefault(c.Raw, "AURION_API_INTERNAL_SECRET", "default_internal_secret"),
	}
}

// GetCoreAPIEnv builds environment variables for the Go Core API service.
func (c *Config) GetCoreAPIEnv() map[string]string {
	// Traitement du BASE_DN pour transformer "domaine.tld" en "dc=domaine,dc=tld"
	domainParts := strings.Split(c.Domain, ".")
	dcParts := make([]string, len(domainParts))
	for i, part := range domainParts {
		dcParts[i] = fmt.Sprintf("dc=%s", part)
	}
	baseDN := fmt.Sprintf("ou=people,%s", strings.Join(dcParts, ","))

	// Configuration Postgres
	pgHost := getOrDefault(c.Raw, "AURION_POSTGRES_HOST", "localhost")
	pgPort := getOrDefault(c.Raw, "AURION_POSTGRES_PORT", "5432")
	pgUser := getOrDefault(c.Raw, "AURION_POSTGRES_USER", "aurionuser")
	pgPass := getOrDefault(c.Raw, "AURION_POSTGRES_PASSWORD", "AURION_DB_PASSWORD")
	pgDB := getOrDefault(c.Raw, "AURION_POSTGRES_DB", "auriondb")
	pgSSL := getOrDefault(c.Raw, "AURION_POSTGRES_SSLMODE", "disable")

	dbURL := c.Raw["CORE_API_DATABASE_URL"]
	if dbURL == "" {
		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
			pgUser, pgPass, pgHost, pgPort, pgDB, pgSSL)
	}

	allowedOrigins := fmt.Sprintf("https://web.%s,https://pad.%s,https://sand.%s",
		c.Domain, c.Domain, c.Domain)

	return map[string]string{
		"PORT":            "8070",
		"ENV":             "production",
		"DOMAIN":          c.Domain,
		"DATA_DIR":        filepath.Join(c.DataDir, "storage", "core"),
		"DATABASE_URL":    dbURL,
		"DB_HOST":         pgHost,
		"DB_PORT":         pgPort,
		"DB_USER":         pgUser,
		"DB_PASSWORD":     pgPass,
		"DB_NAME":         pgDB,
		"DB_SSLMODE":      pgSSL,
		"JWT_SECRET":      getOrDefault(c.Raw, "AURION_JWT_SECRET", "default_jwt_secret"),
		"INTERNAL_SECRET": getOrDefault(c.Raw, "AURION_API_INTERNAL_SECRET", "default_internal_secret"),
		"LDAP_URL":        getOrDefault(c.Raw, "LDAP_URL", "ldap://127.0.0.1:3890"),
		"LDAP_BASE_DN":    getOrDefault(c.Raw, "LDAP_BASE_DN", baseDN),
		"LDAP_USER_ATTR":  getOrDefault(c.Raw, "LDAP_USER_ATTR", "uid"),
		"ALLOWED_ORIGINS": getOrDefault(c.Raw, "ALLOWED_ORIGINS", allowedOrigins),
	}
}

// GetWebmailEnv builds environment variables for BulwarkMail / Web App.
func (c *Config) GetWebmailEnv() map[string]string {
	webmailDataDir := filepath.Join(c.DataDir, "storage", "webmail")

	return map[string]string{
		"PORT":                "3000",
		"NODE_ENV":            "production",
		"SETTINGS_DATA_DIR":   filepath.Join(webmailDataDir, "settings"),
		"ADMIN_CONFIG_DIR":    filepath.Join(webmailDataDir, "admin"),
		"ADMIN_STATE_DIR":     filepath.Join(webmailDataDir, "admin-state"),
		"OAUTH_ENABLED":       "true",
		"OAUTH_ONLY":          "true",
		"OAUTH_CLIENT_ID":     getOrDefault(c.Raw, "WEBMAIL_OAUTH_CLIENT_ID", "stalwart"),
		"OAUTH_CLIENT_SECRET": getOrDefault(c.Raw, "WEBMAIL_OAUTH_CLIENT_SECRET", ""),
		"OAUTH_ISSUER_URL":    getOrDefault(c.Raw, "WEBMAIL_OAUTH_ISSUER_URL", fmt.Sprintf("https://oauth.%s", c.Domain)),
	}
}

func getOrDefault(m map[string]string, key, fallback string) string {
	if val, ok := m[key]; ok && val != "" {
		return val
	}
	return fallback
}
