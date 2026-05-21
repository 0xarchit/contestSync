package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"

	"github.com/0xarchit/contestsync/config"
	"github.com/0xarchit/contestsync/internal/auth"
	"github.com/0xarchit/contestsync/internal/db"
	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/0xarchit/contestsync/internal/sync"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	cfg := config.Load()

	if len(cfg.EncryptionKey) != 32 {
		log.Fatal("ENCRYPTION_KEY must be exactly 32 bytes")
	}

	ctx := context.Background()
	pool, err := db.Init(ctx, cfg.DatabaseURL, cfg.CACertificate, cfg.ConnectionLimit)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

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

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	slog.Info("worker starting", "port", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, mux); err != nil {
		log.Fatal(err)
	}
}
