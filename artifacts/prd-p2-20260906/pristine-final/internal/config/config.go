package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Env is "development" (default) or "production". Production refuses to
	// start on unsafe defaults instead of only logging about them.
	Env             string
	Addr            string
	BaseURL         string
	DatabaseURL     string
	RedisAddr       string
	JWTSecret       string
	AdminUser       string
	AdminPass       string
	TokenTTL        time.Duration
	BannedWordsFile string
	StatsTZ         string // IANA timezone for daily-stat boundaries
	LogRetentionDays int   // structured logs kept this many days

	// LogHTTPAll records every API request instead of only failures and slow
	// ones. Useful while debugging; expensive in production.
	LogHTTPAll bool

	// TrustProxy makes rate limiting read X-Forwarded-For. Enable it only
	// behind a proxy that overwrites the header, or per-IP limits become
	// trivially forgeable.
	TrustProxy bool

	// FuncHTTPAllowPrivate disables the cloud-function SSRF guard (dev only).
	FuncHTTPAllowPrivate bool

	// Artifact storage for the update-distribution subsystem.
	StorageDriver   string // "local" or "s3"
	LocalDataDir    string
	S3Endpoint      string
	S3Region        string
	S3Bucket        string
	S3AccessKey     string
	S3SecretKey     string
	S3UseSSL        bool
	MaxArtifactSize int64 // bytes
}

// DefaultJWTSecret is the development signing key; running with it means any
// token can be forged by anyone who has read the source.
const DefaultJWTSecret = "dev-secret-change-me"

// DefaultAdminPass is the password in the README and compose file.
const DefaultAdminPass = "admin123"

// Production reports whether unsafe defaults should be fatal.
func (c Config) Production() bool { return c.Env == "production" }

// Validate reports every configuration problem at once, so a failed deploy
// does not turn into a sequence of restart-and-discover-the-next-one.
//
// In development these are warnings; in production they are errors, because
// each one means the deployment is trivially compromised.
func (c Config) Validate() (problems []string) {
	if c.JWTSecret == DefaultJWTSecret {
		problems = append(problems, "JWT_SECRET is the shipped default: anyone can forge tokens")
	} else if len(c.JWTSecret) < 32 {
		problems = append(problems, "JWT_SECRET is shorter than 32 characters")
	}
	if c.AdminPass == DefaultAdminPass {
		problems = append(problems, "ADMIN_PASSWORD is the shipped default: anyone can sign into the console")
	}
	if c.FuncHTTPAllowPrivate {
		problems = append(problems,
			"FUNC_HTTP_ALLOW_PRIVATE is on: cloud functions and webhooks can reach your internal network")
	}
	if c.StorageDriver == "local" && strings.HasPrefix(c.BaseURL, "http://localhost") {
		problems = append(problems,
			"BASE_URL still points at localhost: artifact download links will not work for clients")
	}
	return problems
}

func FromEnv() Config {
	return Config{
		Env:             getenv("MINICLOUD_ENV", "development"),
		Addr:            getenv("MINICLOUD_ADDR", ":8080"),
		BaseURL:         getenv("BASE_URL", "http://localhost:8080"),
		DatabaseURL:     getenv("DATABASE_URL", "postgres://minicloud:minicloud@localhost:5432/minicloud"),
		RedisAddr:       getenv("REDIS_ADDR", "localhost:6379"),
		JWTSecret:       getenv("JWT_SECRET", DefaultJWTSecret),
		AdminUser:       getenv("ADMIN_USERNAME", "admin"),
		AdminPass:       getenv("ADMIN_PASSWORD", "admin123"),
		TokenTTL:        getdur("TOKEN_TTL", 7*24*time.Hour),
		BannedWordsFile: os.Getenv("BANNED_WORDS_FILE"),
		StatsTZ:              getenv("STATS_TZ", "Asia/Shanghai"),
		LogRetentionDays:     int(getint64("LOG_RETENTION_DAYS", 14)),
		LogHTTPAll:           getenv("LOG_HTTP_ALL", "false") == "true",
		TrustProxy:           getenv("TRUST_PROXY", "false") == "true",
		FuncHTTPAllowPrivate: getenv("FUNC_HTTP_ALLOW_PRIVATE", "false") == "true",
		StorageDriver:   getenv("STORAGE_DRIVER", "local"),
		LocalDataDir:    getenv("LOCAL_DATA_DIR", "data"),
		S3Endpoint:      os.Getenv("S3_ENDPOINT"),
		S3Region:        getenv("S3_REGION", "us-east-1"),
		S3Bucket:        os.Getenv("S3_BUCKET"),
		S3AccessKey:     os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:     os.Getenv("S3_SECRET_KEY"),
		S3UseSSL:        getenv("S3_USE_SSL", "true") == "true",
		MaxArtifactSize: getint64("MAX_ARTIFACT_SIZE", 2<<30), // 2 GiB
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getint64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func getdur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
