package proxy

import (
	"embed"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Config holds the root domain and routing options.
type Config struct {
	Domain string // e.g., "example.com"
}

// ServiceProxy handles URL routing and serves hydrated HTML bridge templates.
type ServiceProxy struct {
	domain          string
	routes          map[string]*httputil.ReverseProxy
	bridgeTemplates map[string]string // In-memory hydrated HTML bridges
}

// NewProxy initializes the Reverse Proxy and hydrates embedded HTML bridge templates.
func NewProxy(cfg Config, embedFS embed.FS) (*ServiceProxy, error) {
	sp := &ServiceProxy{
		domain:          strings.TrimSpace(cfg.Domain),
		routes:          make(map[string]*httputil.ReverseProxy),
		bridgeTemplates: make(map[string]string),
	}

	// 1. Map internal service target addresses
	targets := map[string]string{
		"api":         "http://127.0.0.1:8070",
		"openpgpkey": "http://127.0.0.1:8070",
		"oauth":       "http://127.0.0.1:4444",
		"pad":         "http://127.0.0.1:3010",
		"sand":        "http://127.0.0.1:3010",
		"sso":         "http://127.0.0.1:3030",
		"web":         "http://127.0.0.1:3000",
		"crypt_ws":    "http://127.0.0.1:3013",
	}

	// 2. Instantiate Reverse Proxies with native WebSocket support
	for sub, rawURL := range targets {
		targetURL, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("invalid target URL for %s: %w", sub, err)
		}

		// Use modern httputil.ReverseProxy setup
		proxy := &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.SetURL(targetURL)
				r.Out.Header.Set("X-Forwarded-Host", r.In.Host)
				if clientIP, _, err := net.SplitHostPort(r.In.RemoteAddr); err == nil {
					r.Out.Header.Set("X-Real-IP", clientIP)
				}
			},
		}

		sp.routes[sub] = proxy
	}

	// 3. Preload and hydrate embedded HTML bridges
	if err := sp.loadBridges(embedFS); err != nil {
		return nil, fmt.Errorf("failed to load bridges: %w", err)
	}

	return sp, nil
}

// loadBridges reads embedded HTML files and injects the configured DOMAIN value.
func (sp *ServiceProxy) loadBridges(embedFS embed.FS) error {
	bridgeFiles := map[string]string{
		"cryptpad_main":    "bridges/cryptpad_main_bridge.html",
		"cryptpad_sandbox": "bridges/cryptpad_sandbox_bridge.html",
		"sso":              "bridges/sso_bridge.html",
		"webmail":          "bridges/webmail_bridge.html",
	}

	for key, path := range bridgeFiles {
		content, err := embedFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("unable to read embedded path %s: %w", path, err)
		}

		// Dynamically replace domain placeholders inside bridge templates
		hydrated := strings.ReplaceAll(string(content), "DOMAIN_TO_REPLACE", sp.domain)
		sp.bridgeTemplates[key] = hydrated
	}

	return nil
}

// ServeHTTP acts as the main HTTP entry point for the orchestrator (listening on port 8080).
func (sp *ServiceProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract subdomain from the Host header (e.g., "pad.example.com" -> "pad")
	host := r.Host
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx] // Strip port if present
	}

	subdomain := strings.TrimSuffix(host, "."+sp.domain)

	switch subdomain {
	case "pad", "sand":
		sp.handleCryptpadRoutes(w, r, subdomain)

	case "sso":
		if r.URL.Path == "/sso_bridge.html" {
			sp.serveBridge(w, "sso", fmt.Sprintf("frame-ancestors https://web.%s", sp.domain))
			return
		}
		sp.routes["sso"].ServeHTTP(w, r)

	case "web":
		if r.URL.Path == "/bridge-minimal.html" {
			sp.serveBridge(w, "webmail", fmt.Sprintf("frame-ancestors https://sso.%s", sp.domain))
			return
		}
		sp.routes["web"].ServeHTTP(w, r)

	default:
		// Generic routing for api, ldap, and oauth services
		if proxy, ok := sp.routes[subdomain]; ok {
			proxy.ServeHTTP(w, r)
			return
		}
		http.Error(w, "Unknown subdomain", http.StatusNotFound)
	}
}

// handleCryptpadRoutes handles CryptPad-specific Nginx routing rules programmatically.
func (sp *ServiceProxy) handleCryptpadRoutes(w http.ResponseWriter, r *http.Request, subdomain string) {
	// 1. Custom login redirect logic
	if r.URL.Path == "/login" && r.URL.Query().Get("from") != "aurion" {
		http.Redirect(w, r, fmt.Sprintf("https://web.%s", sp.domain), http.StatusFound)
		return
	}

	// 2. Intercept HTML Bridge requests and apply dynamic CSP headers
	if r.URL.Path == "/bridge-minimal.html" {
		csp := fmt.Sprintf("frame-ancestors https://web.%s https://sso.%s", sp.domain, sp.domain)
		sp.serveBridge(w, "cryptpad_main", csp)
		return
	}

	if r.URL.Path == "/bridge-sand.html" {
		csp := fmt.Sprintf("frame-ancestors https://pad.%s https://web.%s https://sso.%s", sp.domain, sp.domain, sp.domain)
		sp.serveBridge(w, "cryptpad_sandbox", csp)
		return
	}

	// 3. Route WebSocket connections dedicated to CryptPad (port 3013)
	if strings.HasPrefix(r.URL.Path, "/cryptpad_websocket") {
		sp.routes["crypt_ws"].ServeHTTP(w, r)
		return
	}

	// 4. Standard CryptPad traffic (port 3010)
	sp.routes["pad"].ServeHTTP(w, r)
}

// serveBridge writes the pre-hydrated HTML bridge with its corresponding CSP header.
func (sp *ServiceProxy) serveBridge(w http.ResponseWriter, bridgeKey, cspHeader string) {
	content, exists := sp.bridgeTemplates[bridgeKey]
	if !exists {
		http.Error(w, "Bridge template not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", cspHeader)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}
