package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zekumo/internal/config"
	"zekumo/internal/server"
	"zekumo/internal/storage"
	"zekumo/internal/store"
)

func main() {
	cfg := config.FromEnv()

	// Report every configuration problem before doing any work. In production
	// they are fatal: each one means the deployment is trivially compromised,
	// and a warning in a log nobody reads is not a safeguard.
	if problems := cfg.Validate(); len(problems) > 0 {
		for _, p := range problems {
			if cfg.Production() {
				log.Printf("FATAL config: %s", p)
			} else {
				log.Printf("WARNING config: %s", p)
			}
		}
		if cfg.Production() {
			log.Fatalf("refusing to start in production with %d unsafe setting(s); "+
				"fix them or run without ZEKUMO_ENV=production", len(problems))
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.RedisAddr)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	blob, err := buildStorage(ctx, cfg)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	handler, waitForWorkers := server.New(ctx, cfg, st, blob)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: it would cap WebSocket connections and artifact
		// downloads at a fixed lifetime. Per-request budgets are enforced by
		// the handlers instead (cloud functions carry their own deadline).
	}

	// Bind before announcing: logging "listening" and only then failing on a
	// port clash is actively misleading during a deploy.
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		log.Fatalf("listen %s: %v", cfg.Addr, err)
	}
	log.Printf("zekumo %s listening on %s (env: %s, storage: %s, console at /admin/)",
		server.Version, ln.Addr(), cfg.Env, cfg.StorageDriver)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve: %v", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		log.Println("shutting down…")
		// Stop accepting, let in-flight requests finish, then wait for the
		// background workers to flush before the deferred st.Close() pulls the
		// connection pool out from under them.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		waitForWorkers(shutdownCtx)
		log.Println("stopped")
	}
}

// buildStorage picks the artifact store for the update-distribution
// subsystem: S3-compatible object storage, or local disk served by this
// process (signed with the JWT secret).
func buildStorage(ctx context.Context, cfg config.Config) (storage.Storage, error) {
	if cfg.StorageDriver == "s3" {
		s3, err := storage.NewS3(cfg.S3Endpoint, cfg.S3Region, cfg.S3Bucket,
			cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3UseSSL)
		if err != nil {
			return nil, err
		}
		if err := s3.EnsureBucket(ctx); err != nil {
			return nil, err
		}
		return s3, nil
	}
	return storage.NewLocal(cfg.LocalDataDir, cfg.BaseURL, cfg.JWTSecret, cfg.MaxArtifactSize)
}
