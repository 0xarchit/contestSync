package main

import (
	"context"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0xarchit/contestsync/config"
	"github.com/0xarchit/contestsync/internal/api"
	"github.com/0xarchit/contestsync/internal/auth"
	"github.com/0xarchit/contestsync/internal/db"
	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/0xarchit/contestsync/internal/scheduler"
	"github.com/0xarchit/contestsync/internal/sync"
	ui "github.com/0xarchit/contestsync/web"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/sessions"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	godotenv.Load()

	cfg := config.Load()

	if len(cfg.EncryptionKey) != 32 {
		log.Fatal("ENCRYPTION_KEY must be exactly 32 bytes")
	}

	if len(cfg.SessionSecret) == 0 {
		log.Fatal("SESSION_SECRET must be configured and non-empty")
	}

	shutdownCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	pool, err := db.Init(shutdownCtx, cfg.DatabaseURL, cfg.CACertificate, cfg.ConnectionLimit)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	var valkeyClient *redis.Client
	if cfg.ValkeyURI != "" {
		opt, err := redis.ParseURL(cfg.ValkeyURI)
		if err != nil {
			log.Fatalf("failed to parse VALKEY_URI: %v", err)
		}
		valkeyClient = redis.NewClient(opt)
		if err := valkeyClient.Ping(shutdownCtx).Err(); err != nil {
			log.Fatalf("failed to connect to Valkey: %v", err)
		}
		slog.Info("connected to Valkey successfully")
		defer valkeyClient.Close()
	}

	var sessionStore sessions.Store
	if valkeyClient != nil {
		sessionStore = api.NewValkeyStore(valkeyClient, cfg.Env, cfg.SessionSecret)
	} else {
		cookieStore := sessions.NewCookieStore(cfg.SessionSecret)
		cookieStore.Options = &sessions.Options{
			Path:     "/",
			MaxAge:   86400 * 7,
			HttpOnly: true,
			Secure:   cfg.Env == "production",
			SameSite: http.SameSiteLaxMode,
		}
		sessionStore = cookieStore
	}

	authProvider := auth.NewProvider(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)

	syncer := &sync.Syncer{
		DB:            pool,
		AuthProvider:  authProvider,
		SessionSecret: cfg.EncryptionKey,
		Valkey:        valkeyClient,
	}

	q, err := queue.New(cfg, pool, syncer)
	if err != nil {
		log.Fatalf("failed to initialize kafka queue: %v", err)
	}
	defer q.Close()
	q.StartConsumers(shutdownCtx, cfg)

	handlers := &api.Handlers{
		DB:            pool,
		SessionStore:  sessionStore,
		AuthProvider:  authProvider,
		SessionSecret: cfg.EncryptionKey,
		Queue:         q,
	}

	sched := scheduler.New(pool, q)
	sched.Start()
	defer sched.Stop()

	adminHandlers := &api.AdminHandlers{
		Scheduler:     sched,
		AdminPassword: cfg.AdminPassword,
		DB:            pool,
		Valkey:        valkeyClient,
		Queue:         q,
	}

	r := chi.NewRouter()

	r.Use(api.RequestIDMiddleware)
	r.Use(api.SecurityHeadersMiddleware(cfg.Env))
	r.Use(api.RateLimitMiddleware(valkeyClient, 60, time.Minute))
	r.Use(api.RequestLoggerMiddleware)
	r.Use(middleware.Recoverer)

	r.Get("/health", adminHandlers.HealthCheck)

	r.Get("/auth/google", handlers.GoogleLogin)
	r.Get("/auth/google/callback", handlers.GoogleCallback)

	r.Get("/admin/update", adminHandlers.UpdateContests)
	r.Get("/admin/sync", adminHandlers.SyncAll)

	r.Group(func(r chi.Router) {
		r.Use(api.RequireAuth(sessionStore))
		r.Get("/me", handlers.Me)
		r.Get("/platforms", handlers.GetPlatforms)

		r.Group(func(r chi.Router) {
			r.Use(api.CSRFMiddleware(sessionStore))
			r.Post("/preferences", handlers.SavePreferences)
			r.Post("/sync", handlers.ManualSync)
			r.Delete("/account", handlers.DeleteAccount)
		})
	})

	staticSub, err := fs.Sub(ui.StaticFS, "static")
	if err != nil {
		log.Fatal(err)
	}
	serverFS := http.FileServer(http.FS(staticSub))
	r.Handle("/*", serverFS)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		slog.Info("server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	<-shutdownCtx.Done()
	slog.Info("shutting down server gracefully")

	shutdownTimeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelTimeout()

	if err := srv.Shutdown(shutdownTimeoutCtx); err != nil {
		slog.Error("server shutdown failed", "error", err)
	}

	slog.Info("server stopped")
}
