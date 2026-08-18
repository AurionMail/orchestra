package assets

import (
	"embed"
)

// BridgeTemplatesFS embeds the HTML bridge template files.
// These files are hydrated with runtime domain names and served by internal/proxy.
//
//go:embed bridges/*.html
var BridgeTemplatesFS embed.FS

// RuntimeFS embeds extracted executables and application bundles.
//
// Expected embedded directory structure at build time:
//
// assets/
// ├── node            <-- Portable Node.js binary
// ├── hydra           <-- Ory Hydra binary
// ├── core-api        <-- Core API Go binary
// └── apps/
//
//	├── sso/             <-- Compiled Next.js / Node SSO bundle
//	├── cryptpad/        <-- CryptPad application bundle
//	└── webmail/         <-- BulwarkMail / Web app bundle
//
// Note: During local development, place a dummy file (e.g. assets/.gitkeep)
// so the compiler does not fail if the assets directory is initially empty.
//
//go:embed all:assets
var RuntimeFS embed.FS
