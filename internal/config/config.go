// Package config loads application configuration from environment variables.
// A .env file in the working directory is automatically loaded (via godotenv/autoload).
package config

import (
	"os"

	_ "github.com/joho/godotenv/autoload"
)

// Config holds all runtime configuration for the web server.
type Config struct {
	// Port is the HTTP port to listen on. Defaults to "3000".
	Port string

	// APIURL is the base URL of the API service.
	// In Railway, set this to the internal private URL for zero-latency proxying.
	// Defaults to "http://localhost:8080".
	APIURL string

	// SSRServerURL is the URL of the Node SSR sidecar.
	// Defaults to "http://localhost:3010/render".
	SSRServerURL string

	// SSRScript is the path to ssr-server.mjs.
	// Defaults to the directory containing the running binary + "/ssr-server.mjs".
	SSRScript string

	// SiteBase is the canonical public URL (e.g. "https://example.com").
	// Used for OG tag generation and canonical link hrefs.
	SiteBase string

	// AppName is the human-readable application name (e.g. "My App").
	// Used in OG templates and HTML titles.
	AppName string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() Config {
	return Config{
		Port:         getEnv("PORT", "3000"),
		APIURL:       getEnv("API_URL", "http://localhost:8080"),
		SSRServerURL: getEnv("SSR_SERVER_URL", "http://localhost:3010/render"),
		SSRScript:    os.Getenv("SSR_SCRIPT"), // empty = auto-detect from executable dir
		SiteBase:     getEnv("SITE_BASE", "http://localhost:3000"),
		AppName:      getEnv("APP_NAME", "My App"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
