package config

import "testing"

// --- envOr ---

func TestEnvOr_UsesEnvValueWhenSet(t *testing.T) {
	t.Setenv("CONFIG_TEST_KEY", "from-env")
	if got := envOr("CONFIG_TEST_KEY", "fallback"); got != "from-env" {
		t.Errorf("envOr() = %q, want %q", got, "from-env")
	}
}

func TestEnvOr_UsesFallbackWhenUnset(t *testing.T) {
	// Not calling t.Setenv at all -- key is absent.
	if got := envOr("CONFIG_TEST_KEY_NEVER_SET", "fallback"); got != "fallback" {
		t.Errorf("envOr() = %q, want %q", got, "fallback")
	}
}

func TestEnvOr_EmptyEnvValueUsesFallback(t *testing.T) {
	// Mirrors os.Getenv's own "unset and empty-string are indistinguishable"
	// behavior -- an explicitly empty var still falls back.
	t.Setenv("CONFIG_TEST_KEY", "")
	if got := envOr("CONFIG_TEST_KEY", "fallback"); got != "fallback" {
		t.Errorf("envOr() = %q, want fallback %q for an empty env value", got, "fallback")
	}
}

// --- Load ---

func TestLoad_ReadsAllConfiguredEnvVars(t *testing.T) {
	t.Setenv("DATABASE_URL", "jdbc:postgresql://host/db")
	t.Setenv("DATABASE_USERNAME", "svc_user")
	t.Setenv("DATABASE_PASSWORD", "svc_pass")
	t.Setenv("PORT", "9093")
	t.Setenv("TMDB_API_KEY", "tmdb-key")
	t.Setenv("TMDB_BASE_URL", "https://tmdb.example.com")
	t.Setenv("GRAFANA_OTLP_ENABLED", "true")
	t.Setenv("GRAFANA_OTLP_ENDPOINT", "https://otlp.example.com")
	t.Setenv("GRAFANA_OTLP_AUTH", "Basic abc123")

	cfg := Load()

	want := Config{
		DatabaseURL:     "jdbc:postgresql://host/db",
		DBUsername:      "svc_user",
		DBPassword:      "svc_pass",
		Port:            "9093",
		TMDBAPIKey:      "tmdb-key",
		TMDBBaseURL:     "https://tmdb.example.com",
		GrafanaEnabled:  true,
		GrafanaEndpoint: "https://otlp.example.com",
		GrafanaAuth:     "Basic abc123",
	}
	if cfg != want {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

// TestLoad_DefaultPort_9999_NotDockerfilesExposedPort documents a real,
// preserved-as-is quirk in the Java original: the Dockerfile EXPOSEs 8888,
// but the application itself actually binds to server.port=9999 -- a
// pre-existing mismatch in the source service, reproduced verbatim rather
// than "fixed" to match the other Lumo services' convention.
func TestLoad_DefaultPort_9999_NotDockerfilesExposedPort(t *testing.T) {
	t.Setenv("PORT", "")
	if got := Load().Port; got != "9999" {
		t.Errorf("Port = %q, want default %q", got, "9999")
	}
}

func TestLoad_DefaultsWhenUnset(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_USERNAME", "")
	t.Setenv("DATABASE_PASSWORD", "")
	t.Setenv("PORT", "")
	t.Setenv("TMDB_API_KEY", "")
	t.Setenv("TMDB_BASE_URL", "")
	t.Setenv("GRAFANA_OTLP_ENABLED", "")
	t.Setenv("GRAFANA_OTLP_ENDPOINT", "")
	t.Setenv("GRAFANA_OTLP_AUTH", "")

	cfg := Load()

	if cfg.TMDBBaseURL != "https://api.themoviedb.org" {
		t.Errorf("TMDBBaseURL = %q, want default %q", cfg.TMDBBaseURL, "https://api.themoviedb.org")
	}
	if cfg.GrafanaEnabled {
		t.Error("GrafanaEnabled = true, want false when GRAFANA_OTLP_ENABLED is unset")
	}
	if cfg.DatabaseURL != "" || cfg.DBUsername != "" || cfg.DBPassword != "" ||
		cfg.TMDBAPIKey != "" || cfg.GrafanaEndpoint != "" || cfg.GrafanaAuth != "" {
		t.Errorf("expected all non-defaulted fields to be empty, got %+v", cfg)
	}
}

// TestLoad_GrafanaEnabled_OnlyExactStringTrue mirrors the `== "true"` check
// -- "1", "TRUE", "yes" etc. must NOT enable it, matching a literal string
// comparison rather than strconv.ParseBool semantics.
func TestLoad_GrafanaEnabled_OnlyExactStringTrue(t *testing.T) {
	cases := []struct {
		envVal string
		want   bool
	}{
		{"true", true},
		{"TRUE", false},
		{"1", false},
		{"false", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run("value="+tc.envVal, func(t *testing.T) {
			t.Setenv("GRAFANA_OTLP_ENABLED", tc.envVal)
			if got := Load().GrafanaEnabled; got != tc.want {
				t.Errorf("GrafanaEnabled = %v for GRAFANA_OTLP_ENABLED=%q, want %v", got, tc.envVal, tc.want)
			}
		})
	}
}
