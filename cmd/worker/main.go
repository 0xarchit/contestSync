package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
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
	"github.com/0xarchit/contestsync/internal/observability"
	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/0xarchit/contestsync/internal/sync"
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

	if len(cfg.EncryptionKey) != 32 {
		log.Fatal("ENCRYPTION_KEY must be exactly 32 bytes")
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

	authProvider := auth.NewProvider(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)

	syncer := &sync.Syncer{
		DB:            pool.WriteDB(),
		ReadDB:        pool.ReadDB(),
		AuthProvider:  authProvider,
		SessionSecret: cfg.EncryptionKey,
		Valkey:        valkeyClient,
	}

	q, err := queue.New(cfg, pool.WriteDB(), syncer)
	if err != nil {
		log.Fatalf("failed to initialize kafka queue: %v", err)
	}
	defer q.Close()
	q.StartConsumers(shutdownCtx, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><head><title>ContestSync Worker</title></head><body><h1>ContestSync Worker Active...</h1></body></html>"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			adminPass := r.Header.Get("X-Admin-Password")
			if len(adminPass) > 256 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			if cfg.AdminPassword == "" || subtle.ConstantTimeCompare([]byte(adminPass), []byte(cfg.AdminPassword)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			checkCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			services := make(map[string]map[string]any)
			allHealthy := true
			pgStart := time.Now()
			var pgErr error
			if pool != nil {
				if pool.ReadDB() != nil {
					var result int
					pgErr = pool.ReadDB().QueryRow(checkCtx, "SELECT 1 + 1").Scan(&result)
				}
				if pgErr == nil {
					tx, err := pool.WriteDB().Begin(checkCtx)
					if err == nil {
						tx.Rollback(checkCtx)
					} else {
						pgErr = err
					}
				}
			} else {
				pgErr = fmt.Errorf("database pool not initialized")
			}
			pgLatency := float64(time.Since(pgStart).Microseconds()) / 1000.0
			if pgErr != nil {
				services["postgres"] = map[string]any{"status": "unhealthy", "latency_ms": pgLatency, "error": pgErr.Error()}
				allHealthy = false
			} else {
				services["postgres"] = map[string]any{"status": "healthy", "latency_ms": pgLatency}
			}
			if valkeyClient != nil {
				vkStart := time.Now()
				vkErr := valkeyClient.Ping(checkCtx).Err()
				vkLatency := float64(time.Since(vkStart).Microseconds()) / 1000.0
				if vkErr != nil {
					services["valkey"] = map[string]any{"status": "unhealthy", "latency_ms": vkLatency, "error": vkErr.Error()}
					allHealthy = false
				} else {
					services["valkey"] = map[string]any{"status": "healthy", "latency_ms": vkLatency}
				}
			} else {
				services["valkey"] = map[string]any{"status": "not_configured", "latency_ms": 0.0}
			}
			if q != nil {
				qStart := time.Now()
				qErr := q.Health(checkCtx)
				qLatency := float64(time.Since(qStart).Microseconds()) / 1000.0
				if qErr != nil {
					services["kafka"] = map[string]any{"status": "unhealthy", "latency_ms": qLatency, "error": qErr.Error()}
					allHealthy = false
				} else {
					services["kafka"] = map[string]any{"status": "healthy", "latency_ms": qLatency}
				}
			} else {
				services["kafka"] = map[string]any{"status": "not_configured", "latency_ms": 0.0}
			}
			w.Header().Set("Content-Type", "application/json")
			status := "ok"
			if !allHealthy {
				status = "degraded"
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			json.NewEncoder(w).Encode(map[string]any{"status": status, "services": services})
			return
		}
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
		if tgManager != nil {
			tgManager.TriggerSystemEvent("STARTUP", "Worker starting on port "+cfg.WorkerPort)
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start worker: %v", err)
		}
	}()

	<-shutdownCtx.Done()
	slog.Info("shutting down worker gracefully")
	if tgManager != nil {
		tgManager.TriggerSystemEvent("SHUTDOWN", "Worker is shutting down gracefully")
	}

	shutdownTimeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelTimeout()

	if err := srv.Shutdown(shutdownTimeoutCtx); err != nil {
		slog.Error("worker shutdown failed", "error", err)
	}

	slog.Info("draining consumer queue")
	q.Drain()

	slog.Info("worker stopped")
}
