package main

import (
	"context"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/0xarchit/contestsync/config"
	"github.com/0xarchit/contestsync/internal/api"
	"github.com/0xarchit/contestsync/internal/auth"
	"github.com/0xarchit/contestsync/internal/db"
	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/0xarchit/contestsync/internal/scheduler"

	"github.com/0xarchit/contestsync/internal/sync"
	"github.com/0xarchit/contestsync/web"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/sessions"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()
	cfg := config.Load()

	ctx := context.Background()
	pool, err := db.Init(ctx, cfg.DatabaseURL, cfg.CACertificate, cfg.ConnectionLimit)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	sessionStore := sessions.NewCookieStore(cfg.SessionSecret)
	sessionStore.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		Secure:   cfg.Env == "production",
		SameSite: http.SameSiteLaxMode,
	}

	authProvider := auth.NewProvider(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)

	syncer := &sync.Syncer{
	        DB:            pool,
	        AuthProvider:  authProvider,
	        SessionSecret: cfg.EncryptionKey,
	}

	q, err := queue.New(cfg, pool, syncer)
	if err != nil {
	        log.Fatalf("failed to initialize kafka queue: %v", err)
	}
	q.StartConsumers(ctx, cfg)

	handlers := &api.Handlers{
	        DB:            pool,
	        SessionStore:  sessionStore,
	        AuthProvider:  authProvider,
	        SessionSecret: cfg.EncryptionKey,
	        Queue:         q,
	}

	sched := scheduler.New(pool, q)
	sched.Start()
	adminHandlers := &api.AdminHandlers{
		Scheduler:     sched,
		AdminPassword: cfg.AdminPassword,
	}

	r := chi.NewRouter()

	r.Use(api.RequestIDMiddleware)
	r.Use(api.SecurityHeadersMiddleware(cfg.Env))
	r.Use(api.RateLimitMiddleware(60, time.Minute))
	r.Use(api.RequestLoggerMiddleware)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	r.Get("/auth/google", handlers.GoogleLogin)
	r.Get("/auth/google/callback", handlers.GoogleCallback)

	// Admin Routes
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

	// Embedded Static files
	staticSub, err := fs.Sub(ui.StaticFS, "static")
	if err != nil {
		log.Fatal(err)
	}
	serverFS := http.FileServer(http.FS(staticSub))
	r.Handle("/*", serverFS)

	slog.Info("server starting", "port", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, r); err != nil {
		log.Fatal(err)
	}
}
