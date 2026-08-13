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
	"github.com/0xarchit/contestsync/internal/observability"
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

	logLevelStr := os.Getenv("LOG_LEVEL")
	level := slog.LevelInfo
	if logLevelStr == "debug" || logLevelStr == "DEBUG" {
		level = slog.LevelDebug
	} else if logLevelStr == "warn" || logLevelStr == "WARN" {
		level = slog.LevelWarn
	} else if logLevelStr == "error" || logLevelStr == "ERROR" {
		level = slog.LevelError
	}

	var handler slog.Handler
	if cfg.Env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}
	slog.SetDefault(slog.New(handler))

	var tgManager *observability.Manager
	tgManager, handler = observability.Init(cfg.TelegramProxyURL, cfg.ProxySecretKey, cfg.TelegramGroupID, cfg.TelegramGroupTopicID, cfg.From, handler)
	slog.SetDefault(slog.New(handler))
	if tgManager != nil {
		defer tgManager.Drain()
	}

	otelMetrics := observability.InitOTel("contestsync-server")
	if otelMetrics != nil && otelMetrics.Shutdown != nil {
		defer otelMetrics.Shutdown(context.Background())
	}

	if len(cfg.EncryptionKey) != 32 {
		log.Fatal("ENCRYPTION_KEY must be exactly 32 bytes")
	}

	if len(cfg.SessionSecret) == 0 {
		log.Fatal("SESSION_SECRET must be configured and non-empty")
	}

	if cfg.Env != "development" && cfg.Env != "dev" && cfg.Env != "local" {
		if os.Getenv("HCAPTCHA_SECRET") == "" {
			log.Fatal("HCAPTCHA_SECRET must be configured in production")
		}
	}

	shutdownCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	pool, err := db.Init(shutdownCtx, cfg.DatabaseURL, cfg.ReadDatabaseURLs, cfg.ConnectionLimit, cfg.ConnectionPoolLimit)
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
		opt.ConnMaxIdleTime = 3 * time.Minute
		opt.ConnMaxLifetime = 10 * time.Minute
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
			Secure:   cfg.Env != "development" && cfg.Env != "dev" && cfg.Env != "local",
			SameSite: http.SameSiteLaxMode,
		}
		sessionStore = cookieStore
	}

	authProvider := auth.NewProvider(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)

	syncer := &sync.Syncer{
		DB:            pool.WriteDB(),
		ReadDB:        pool.ReadDB(),
		AuthProvider:  authProvider,
		EncryptionKey: cfg.EncryptionKey,
		Valkey:        valkeyClient,
	}
	if tgManager != nil {
		syncer.OnTelegramEvent = tgManager.TriggerSystemEvent
	}

	q, err := queue.New(cfg, pool.WriteDB(), syncer)
	if err != nil {
		log.Fatalf("failed to initialize queue: %v", err)
	}
	defer q.Close()
	if tgManager != nil {
		q.OnTelegramEvent = tgManager.TriggerSystemEvent
	}
	q.StartConsumers(shutdownCtx, cfg)

	handlers := &api.Handlers{
		DB:            pool.WriteDB(),
		ReadDB:        pool.ReadDB(),
		SessionStore:  sessionStore,
		AuthProvider:  authProvider,
		EncryptionKey: cfg.EncryptionKey,
		Queue:         q,
		Valkey:        valkeyClient,
		Env:           cfg.Env,
	}

	sched := scheduler.New(pool.ReadDB(), pool.WriteDB(), q)
	if tgManager != nil {
		sched.OnEvent = tgManager.TriggerSystemEvent
	}
	sched.Start()
	defer sched.Stop()

	adminHandlers := &api.AdminHandlers{
		Scheduler:     sched,
		AdminPassword: cfg.AdminPassword,
		DB:            pool.WriteDB(),
		ReadDB:        pool.ReadDB(),
		Valkey:        valkeyClient,
		Queue:         q,
	}

	r := chi.NewRouter()

	r.Use(api.RequestIDMiddleware)
	r.Use(api.SecurityHeadersMiddleware(cfg.Env))
	r.Use(api.RateLimitMiddleware(valkeyClient, 300, time.Minute))
	r.Use(middleware.Compress(5))
	r.Use(api.RequestLoggerMiddleware)
	r.Use(middleware.Recoverer)

	r.HandleFunc("/health", adminHandlers.HealthCheck)

	r.Get("/feed/ical", handlers.ServeICalFeed)

	r.Get("/auth/google", handlers.GoogleLogin)
	r.Get("/auth/google/callback", handlers.GoogleCallback)

	r.Group(func(r chi.Router) {
		r.Use(api.RateLimitMiddleware(valkeyClient, 10, 15*time.Minute))
		r.Use(api.CSRFMiddleware(sessionStore))
		r.Post("/admin/update", adminHandlers.UpdateContests)
		r.Post("/admin/sync", adminHandlers.SyncAll)
	})

	r.Group(func(r chi.Router) {
		r.Use(api.RequireAuth(sessionStore))
		r.Get("/me", handlers.Me)
		r.Get("/platforms", handlers.GetPlatforms)

		r.Group(func(r chi.Router) {
			r.Use(api.RateLimitMiddleware(valkeyClient, 20, time.Minute))
			r.Get("/auth/calendar/validate", handlers.ValidateCalendarAccess)
		})

		r.Group(func(r chi.Router) {
			r.Use(api.CSRFMiddleware(sessionStore))
			r.Post("/preferences", handlers.SavePreferences)
			r.Post("/sync", handlers.ManualSync)
			r.Delete("/account", handlers.DeleteAccount)
			r.Post("/auth/logout", handlers.Logout)
		})
	})

	staticSub, err := fs.Sub(ui.StaticFS, "static")
	if err != nil {
		log.Fatal(err)
	}
	serverFS, err := api.NewStaticServer(staticSub)
	if err != nil {
		log.Fatal(err)
	}
	r.Handle("/*", serverFS)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		slog.Info("server starting", "port", cfg.Port)
		if tgManager != nil {
			tgManager.TriggerSystemEvent("STARTUP", "Server starting on port "+cfg.Port)
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	<-shutdownCtx.Done()
	slog.Info("shutting down server gracefully")
	if tgManager != nil {
		tgManager.TriggerSystemEvent("SHUTDOWN", "Server is shutting down gracefully")
	}

	shutdownTimeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelTimeout()

	if err := srv.Shutdown(shutdownTimeoutCtx); err != nil {
		slog.Error("server shutdown failed", "error", err)
	}

	cleanupTimeoutCtx, cancelCleanup := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelCleanup()

	cleanupDone := make(chan struct{})
	go func() {
		handlers.CleanupWG.Wait()
		close(cleanupDone)
	}()
	select {
	case <-cleanupDone:
		slog.Info("all background cleanups finished")
	case <-cleanupTimeoutCtx.Done():
		slog.Warn("timed out waiting for background cleanups to finish")
	}

	slog.Info("draining consumer queue")
	q.Drain()

	slog.Info("server stopped")
}
