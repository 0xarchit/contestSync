package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/0xarchit/contestsync/internal/scheduler"
)

type AdminHandlers struct {
	Scheduler     *scheduler.Scheduler
	AdminPassword string
}

func (h *AdminHandlers) UpdateContests(w http.ResponseWriter, r *http.Request) {
	pass := r.Header.Get("X-Admin-Password")
	if h.AdminPassword == "" || pass != h.AdminPassword {
		slog.Warn("unauthorized admin update attempt", "ip", r.RemoteAddr)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	slog.Info("manual contest update triggered via /admin/update")
	
	go func() {
		h.Scheduler.RunExtraction(context.Background())
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "update triggered"})
}

func (h *AdminHandlers) SyncAll(w http.ResponseWriter, r *http.Request) {
	pass := r.Header.Get("X-Admin-Password")
	if h.AdminPassword == "" || pass != h.AdminPassword {
		slog.Warn("unauthorized admin sync attempt", "ip", r.RemoteAddr)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	slog.Info("manual global sync triggered via /admin/sync")

	go func() {
		h.Scheduler.SyncAllUsers(context.Background())
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "global sync triggered"})
}
