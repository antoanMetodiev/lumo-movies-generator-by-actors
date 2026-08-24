// Package config reads the exact same environment variables the Java
// service used (application.properties), so this drop-in replacement needs
// zero changes to whatever's already set on Render.
package config

import "os"

type Config struct {
	DatabaseURL     string // raw DATABASE_URL (may carry a "jdbc:" prefix), see dbx.Dial
	DBUsername      string
	DBPassword      string
	Port            string
	TMDBAPIKey      string
	TMDBBaseURL     string
	GrafanaEnabled  bool
	GrafanaEndpoint string
	GrafanaAuth     string
}

func Load() Config {
	return Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		DBUsername:  os.Getenv("DATABASE_USERNAME"),
		DBPassword:  os.Getenv("DATABASE_PASSWORD"),
		// Java's server.port=9999 (the Dockerfile's EXPOSE 8888 is a
		// pre-existing mismatch in the original — 9999 is what the app
		// actually binds to).
		Port:            envOr("PORT", "9999"),
		TMDBAPIKey:      os.Getenv("TMDB_API_KEY"),
		TMDBBaseURL:     os.Getenv("TMDB_BASE_URL"),
		GrafanaEnabled:  os.Getenv("GRAFANA_OTLP_ENABLED") == "true",
		GrafanaEndpoint: os.Getenv("GRAFANA_OTLP_ENDPOINT"),
		GrafanaAuth:     os.Getenv("GRAFANA_OTLP_AUTH"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
