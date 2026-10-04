package config

import (
	"strings"
	"testing"
)

func TestValidateFlagsUnsafeDefaults(t *testing.T) {
	// A config straight out of the box must be reported as unsafe.
	cfg := Config{
		JWTSecret:     DefaultJWTSecret,
		AdminPass:     DefaultAdminPass,
		StorageDriver: "local",
		BaseURL:       "http://localhost:8080",
	}
	problems := cfg.Validate()
	for _, want := range []string{"JWT_SECRET", "ADMIN_PASSWORD", "BASE_URL"} {
		if !mentions(problems, want) {
			t.Errorf("Validate() did not flag %s: %v", want, problems)
		}
	}
}

func TestValidateAcceptsHardenedConfig(t *testing.T) {
	cfg := Config{
		JWTSecret:     strings.Repeat("a", 64),
		AdminPass:     "a-real-password",
		StorageDriver: "local",
		BaseURL:       "https://api.example.com",
	}
	if problems := cfg.Validate(); len(problems) != 0 {
		t.Errorf("a hardened config should pass, got: %v", problems)
	}
}

func TestValidateFlagsShortSecretAndPrivateHTTP(t *testing.T) {
	cfg := Config{
		JWTSecret:            "too-short",
		AdminPass:            "a-real-password",
		StorageDriver:        "s3",
		BaseURL:              "https://api.example.com",
		FuncHTTPAllowPrivate: true,
	}
	problems := cfg.Validate()
	if !mentions(problems, "shorter than 32") {
		t.Errorf("a short JWT_SECRET should be flagged: %v", problems)
	}
	if !mentions(problems, "FUNC_HTTP_ALLOW_PRIVATE") {
		t.Errorf("private outbound access should be flagged: %v", problems)
	}
}

func TestProduction(t *testing.T) {
	if (Config{Env: "production"}).Production() != true {
		t.Error(`Env "production" must be production`)
	}
	for _, env := range []string{"", "development", "staging"} {
		if (Config{Env: env}).Production() {
			t.Errorf("Env %q must not be treated as production", env)
		}
	}
}

func mentions(problems []string, substr string) bool {
	for _, p := range problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}
