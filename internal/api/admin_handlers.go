package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/0xarchit/contestsync/internal/scheduler"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type AdminHandlers struct {
	Scheduler     *scheduler.Scheduler
	AdminPassword string
	DB            *pgxpool.Pool
	Valkey        *redis.Client
	Queue         *queue.Queue
}

func (h *AdminHandlers) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	pass := r.Header.Get("X-Admin-Password")
	if h.AdminPassword == "" || subtle.ConstantTimeCompare([]byte(pass), []byte(h.AdminPassword)) != 1 {
		slog.Warn("unauthorized admin attempt", "ip", r.RemoteAddr, "path", r.URL.Path)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	return true
}

func (h *AdminHandlers) UpdateContests(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	slog.Info("manual contest update triggered via /admin/update")
	h.Scheduler.RunExtraction(context.Background())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "update triggered"})
}

func (h *AdminHandlers) SyncAll(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	slog.Info("manual global sync triggered via /admin/sync")
	h.Scheduler.SyncAllUsers(context.Background())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "global sync triggered"})
}

type componentStatus struct {
	Status    string  `json:"status"`
	LatencyMs float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
}

type healthResponse struct {
	Status   string                     `json:"status"`
	Services map[string]componentStatus `json:"services"`
}

func measure(fn func() error) (float64, error) {
	start := time.Now()
	err := fn()
	return float64(time.Since(start).Microseconds()) / 1000.0, err
}

func (h *AdminHandlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	checkCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	services := make(map[string]componentStatus)
	allHealthy := true

	pgLatency, pgErr := measure(func() error {
		return checkPostgres(checkCtx, h.DB)
	})
	if pgErr != nil {
		services["postgres"] = componentStatus{Status: "unhealthy", LatencyMs: pgLatency, Error: pgErr.Error()}
		allHealthy = false
	} else {
		services["postgres"] = componentStatus{Status: "healthy", LatencyMs: pgLatency}
	}

	if h.Valkey != nil {
		vkLatency, vkErr := measure(func() error {
			return checkValkey(checkCtx, h.Valkey)
		})
		if vkErr != nil {
			services["valkey"] = componentStatus{Status: "unhealthy", LatencyMs: vkLatency, Error: vkErr.Error()}
			allHealthy = false
		} else {
			services["valkey"] = componentStatus{Status: "healthy", LatencyMs: vkLatency}
		}
	} else {
		services["valkey"] = componentStatus{Status: "not_configured", LatencyMs: 0}
	}

	if h.Queue != nil {
		kafkaLatency, kafkaErr := measure(func() error {
			return h.Queue.Health(checkCtx)
		})
		if kafkaErr != nil {
			services["kafka"] = componentStatus{Status: "unhealthy", LatencyMs: kafkaLatency, Error: kafkaErr.Error()}
			allHealthy = false
		} else {
			services["kafka"] = componentStatus{Status: "healthy", LatencyMs: kafkaLatency}
		}
	} else {
		services["kafka"] = componentStatus{Status: "not_configured", LatencyMs: 0}
	}

	resp := healthResponse{Services: services}
	w.Header().Set("Content-Type", "application/json")
	if allHealthy {
		resp.Status = "ok"
		w.WriteHeader(http.StatusOK)
	} else {
		resp.Status = "degraded"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(resp)
}

func checkPostgres(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction failed: %w", err)
	}
	defer tx.Rollback(ctx)

	var result int
	err = tx.QueryRow(ctx, "SELECT 1 + 1").Scan(&result)
	if err != nil || result != 2 {
		return fmt.Errorf("read query failed: %w", err)
	}

	_, err = tx.Exec(ctx, "SELECT pg_sleep(0)")
	if err != nil {
		return fmt.Errorf("write-path exec failed: %w", err)
	}

	return nil
}

func checkValkey(ctx context.Context, client *redis.Client) error {
	probeKey := fmt.Sprintf("__health:%d__", time.Now().UnixNano())

	if err := client.Set(ctx, probeKey, "1", 10*time.Second).Err(); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	val, err := client.Get(ctx, probeKey).Result()
	if err != nil {
		return fmt.Errorf("read failed: %w", err)
	}
	if val != "1" {
		return fmt.Errorf("read returned unexpected value: %q", val)
	}

	client.Del(ctx, probeKey)
	return nil
}
