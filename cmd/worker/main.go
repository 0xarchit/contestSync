package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0xarchit/contestsync/config"
	"github.com/0xarchit/contestsync/internal/auth"
	"github.com/0xarchit/contestsync/internal/db"
	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/0xarchit/contestsync/internal/sync"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	godotenv.Load()

	cfg := config.Load()

	if len(cfg.EncryptionKey) != 32 {
		log.Fatal("ENCRYPTION_KEY must be exactly 32 bytes")
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

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	srv := &http.Server{
		Addr:    ":" + cfg.WorkerPort,
		Handler: mux,
	}

	go func() {
		slog.Info("worker starting", "port", cfg.WorkerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start worker: %v", err)
		}
	}()

	<-shutdownCtx.Done()
	slog.Info("shutting down worker gracefully")

	shutdownTimeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelTimeout()

	if err := srv.Shutdown(shutdownTimeoutCtx); err != nil {
		slog.Error("worker shutdown failed", "error", err)
	}

	slog.Info("worker stopped")
}
