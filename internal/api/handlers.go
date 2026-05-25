package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/0xarchit/contestsync/internal/auth"
	"github.com/0xarchit/contestsync/internal/extractor"
	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/0xarchit/contestsync/models"
	"github.com/gorilla/sessions"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
	"github.com/redis/go-redis/v9"
)

type Handlers struct {
	DB            *pgxpool.Pool
	SessionStore  sessions.Store
	AuthProvider  *auth.Provider
	SessionSecret []byte
	Queue         *queue.Queue
	Valkey        *redis.Client
	Env           string
}

func (h *Handlers) ManualSync(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(ContextKeyUserID).(int)
	if err := h.Queue.PublishSyncTask(r.Context(), userID); err != nil {
		slog.Error("manual sync queuing failed", "user_id", userID, "error", err)
		http.Error(w, `{"error":"sync failed to queue"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "sync queued"})
}

func (h *Handlers) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := generateRandomString(32)
	if err != nil {
		slog.Error("failed to generate oauth state", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	_, err = h.DB.Exec(r.Context(), "INSERT INTO oauth_states (state) VALUES ($1)", state)
	if err != nil {
		slog.Error("failed to store oauth state", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	cookie := &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/auth/google/callback",
		HttpOnly: true,
		Secure:   h.Env != "development" && h.Env != "dev" && h.Env != "local",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	}
	http.SetCookie(w, cookie)

	url := h.AuthProvider.Config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (h *Handlers) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie("oauth_state")
	if err != nil {
		http.Error(w, `{"error":"missing state cookie"}`, http.StatusBadRequest)
		return
	}

	if cookie.Value != state {
		http.Error(w, `{"error":"invalid state"}`, http.StatusBadRequest)
		return
	}

	var dbState string
	err = h.DB.QueryRow(r.Context(), "DELETE FROM oauth_states WHERE state = $1 AND created_at > $2 RETURNING state", state, time.Now().Add(-10*time.Minute)).Scan(&dbState)
	if err != nil {
		slog.Warn("invalid or expired oauth state", "state", state, "error", err)
		http.Error(w, `{"error":"invalid state or expired"}`, http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	token, err := h.AuthProvider.Config.Exchange(r.Context(), code)
	if err != nil {
		slog.Error("failed to exchange oauth token", "error", err)
		http.Error(w, `{"error":"authentication failed"}`, http.StatusInternalServerError)
		return
	}

	client := h.AuthProvider.Config.Client(r.Context(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		slog.Error("failed to get user info", "error", err)
		http.Error(w, `{"error":"failed to fetch user info"}`, http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var userInfo struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		slog.Error("failed to decode user info", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	encryptedRefreshToken := ""
	if token.RefreshToken != "" {
		encryptedRefreshToken, err = auth.EncryptToken(token.RefreshToken, h.SessionSecret)
		if err != nil {
			slog.Error("failed to encrypt refresh token", "error", err)
		}
	}

	var userID int
	err = h.DB.QueryRow(r.Context(), `
		INSERT INTO users (google_id, email, refresh_token)
		VALUES ($1, $2, $3)
		ON CONFLICT (google_id) DO UPDATE SET email = $2, refresh_token = CASE WHEN $3 <> '' THEN $3 ELSE users.refresh_token END
		RETURNING id
	`, userInfo.ID, userInfo.Email, encryptedRefreshToken).Scan(&userID)

	if err != nil {
		slog.Error("failed to upsert user", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	if h.Valkey != nil {
		cacheKey := models.UserCacheKey(userID)
		if err := h.Valkey.Del(r.Context(), cacheKey).Err(); err != nil {
			slog.Error("failed to invalidate user cache after oauth upsert", "user_id", userID, "error", err)
		}
	}

	// Create session
	session, err := h.SessionStore.Get(r, "session")
	if err != nil {
		slog.Warn("corrupted session, creating new", "error", err)
		session, _ = h.SessionStore.New(r, "session")
	}

	session.Options.MaxAge = -1
	session.Save(r, w)

	session, _ = h.SessionStore.New(r, "session")
	session.ID = ""
	session.Values["user_id"] = userID

	csrfToken, err := generateRandomString(32)
	if err != nil {
		slog.Error("failed to generate csrf token", "error", err)
	}
	session.Values["csrf_token"] = csrfToken

	if err := session.Save(r, w); err != nil {
		slog.Error("failed to save session", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	slog.Info("user logged in", "user_id", userID)

	// Queue initial sync for new/returning user
	if err := h.Queue.PublishSyncTask(r.Context(), userID); err != nil {
		slog.Error("failed to queue initial sync", "user_id", userID, "error", err)
	}

	http.Redirect(w, r, "/preferences.html", http.StatusSeeOther)
}

func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(ContextKeyUserID)
	if val == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	userID := val.(int)

	var cachedUser models.CachedUser
	cacheKey := models.UserCacheKey(userID)
	cacheFound := false

	if h.Valkey != nil {
		cachedVal, err := h.Valkey.Get(r.Context(), cacheKey).Result()
		if err == nil {
			if err := json.Unmarshal([]byte(cachedVal), &cachedUser); err == nil {
				cacheFound = true
			}
		}
	}

	if !cacheFound {
		var calendarID sql.NullString
		var encryptedRefreshToken string
		err := h.DB.QueryRow(r.Context(), "SELECT id, google_id, email, calendar_id, use_dedicated, platforms, refresh_token FROM users WHERE id = $1", userID).Scan(
			&cachedUser.ID, &cachedUser.GoogleID, &cachedUser.Email, &calendarID, &cachedUser.UseDedicated, &cachedUser.Platforms, &encryptedRefreshToken,
		)
		if err != nil {
			slog.Error("failed to fetch user", "user_id", userID, "error", err)
			http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
			return
		}
		cachedUser.CalendarID = ""
		if calendarID.Valid {
			cachedUser.CalendarID = calendarID.String
		}
		cachedUser.RefreshToken = encryptedRefreshToken

		if h.Valkey != nil {
			if serialized, err := json.Marshal(cachedUser); err == nil {
				if err := h.Valkey.Set(r.Context(), cacheKey, string(serialized), models.UserCacheTTL).Err(); err != nil {
					slog.Error("failed to write user cache", "user_id", userID, "error", err)
				}
			}
		}
	}

	var user struct {
		models.User
		Platforms []string `json:"platforms"`
		CSRFToken string   `json:"csrf_token"`
	}
	user.ID = cachedUser.ID
	user.GoogleID = cachedUser.GoogleID
	user.Email = cachedUser.Email
	user.CalendarID = cachedUser.CalendarID
	user.UseDedicated = cachedUser.UseDedicated
	user.Platforms = cachedUser.Platforms

	session, err := h.SessionStore.Get(r, "session")
	if err == nil {
		if csrf, ok := session.Values["csrf_token"].(string); ok {
			user.CSRFToken = csrf
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func (h *Handlers) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(ContextKeyUserID).(int)

	var encryptedRefreshToken string
	err := h.DB.QueryRow(r.Context(), "SELECT refresh_token FROM users WHERE id = $1", userID).Scan(&encryptedRefreshToken)
	if err == nil && encryptedRefreshToken != "" {
		if refreshToken, decryptErr := auth.DecryptToken(encryptedRefreshToken, h.SessionSecret); decryptErr == nil && refreshToken != "" {
			go func() {
				resp, postErr := http.PostForm("https://oauth2.googleapis.com/revoke", url.Values{"token": {refreshToken}})
				if postErr == nil {
					resp.Body.Close()
				} else {
					slog.Error("failed to revoke oauth token", "error", postErr)
				}
			}()
		}
	}

	_, err = h.DB.Exec(r.Context(), "DELETE FROM users WHERE id = $1", userID)
	if err != nil {
		slog.Error("failed to delete user account", "user_id", userID, "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	if h.Valkey != nil {
		cacheKey := models.UserCacheKey(userID)
		if err := h.Valkey.Del(r.Context(), cacheKey).Err(); err != nil {
			slog.Error("failed to invalidate user cache on account deletion", "user_id", userID, "error", err)
		}
	}

	// Clear session
	session, _ := h.SessionStore.Get(r, "session")
	session.Options.MaxAge = -1
	session.Save(r, w)

	slog.Info("user deleted account", "user_id", userID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (h *Handlers) GetPlatforms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{"platforms": extractor.Platforms})
}

func (h *Handlers) SavePreferences(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(ContextKeyUserID).(int)
	var req struct {
		Platforms    []string `json:"platforms"`
		UseDedicated bool     `json:"use_dedicated"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	allowed := make(map[string]bool)
	for _, p := range extractor.Platforms {
		allowed[p] = true
	}
	for _, p := range req.Platforms {
		if !allowed[p] {
			http.Error(w, `{"error":"invalid platform"}`, http.StatusBadRequest)
			return
		}
	}

	_, err := h.DB.Exec(r.Context(), "UPDATE users SET platforms = $1, use_dedicated = $2 WHERE id = $3", req.Platforms, req.UseDedicated, userID)
	if err != nil {
		slog.Error("failed to update user platforms", "user_id", userID, "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	if h.Valkey != nil {
		cacheKey := models.UserCacheKey(userID)
		if err := h.Valkey.Del(r.Context(), cacheKey).Err(); err != nil {
			slog.Error("failed to invalidate user cache on preference update", "user_id", userID, "error", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
func generateRandomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
