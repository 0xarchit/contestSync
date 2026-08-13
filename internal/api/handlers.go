package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/0xarchit/contestsync/internal/auth"
	"github.com/0xarchit/contestsync/internal/extractor"
	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/0xarchit/contestsync/models"
	"github.com/gorilla/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type Handlers struct {
	DB            *pgxpool.Pool
	ReadDB        *pgxpool.Pool
	SessionStore  sessions.Store
	AuthProvider  *auth.Provider
	EncryptionKey []byte
	Queue         *queue.Queue
	Valkey        *redis.Client
	Env           string
	CleanupWG     sync.WaitGroup
}

func (h *Handlers) ManualSync(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(ContextKeyUserID).(int)

	var lastSyncAt *time.Time
	cacheKey := fmt.Sprintf("user:last_sync_at:%d", userID)
	cacheFound := false

	if h.Valkey != nil {
		val, err := h.Valkey.Get(r.Context(), cacheKey).Result()
		if err == nil && val != "" {
			if t, parseErr := time.Parse(time.RFC3339, val); parseErr == nil {
				lastSyncAt = &t
				cacheFound = true
			}
		}
	}

	if !cacheFound {
		readPool := h.ReadDB
		if readPool == nil {
			readPool = h.DB
		}
		err := readPool.QueryRow(r.Context(), "SELECT last_sync_at FROM users WHERE id = $1", userID).Scan(&lastSyncAt)
		if err != nil {
			slog.Error("failed to check last sync time from read db", "user_id", userID, "error", err)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}

		if h.Valkey != nil && lastSyncAt != nil {
			if err := h.Valkey.Set(r.Context(), cacheKey, lastSyncAt.Format(time.RFC3339), 1*time.Hour).Err(); err != nil {
				slog.Error("failed to cache last sync time", "user_id", userID, "error", err)
			}
		}
	}

	if lastSyncAt != nil && time.Since(*lastSyncAt) < 1*time.Minute {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "rate_limited",
			"message": "Please wait 1 minute between manual syncs.",
		})
		return
	}

	if err := h.Queue.PublishSyncTask(r.Context(), userID); err != nil {
		slog.Error("manual sync queuing failed", "user_id", userID, "error", err)
		http.Error(w, `{"error":"sync failed to queue"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "sync queued"})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	session, err := h.SessionStore.Get(r, "session")
	if err == nil {
		session.Options.MaxAge = -1
		if err := session.Save(r, w); err != nil {
			slog.Error("failed to clear session on logout", "error", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "logged out"})
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
		encryptedRefreshToken, err = auth.EncryptToken(token.RefreshToken, h.EncryptionKey)
		if err != nil {
			slog.Error("failed to encrypt refresh token", "error", err)
		}
	}

	var userID int
	var platforms []string
	err = h.DB.QueryRow(r.Context(), `
		INSERT INTO users (google_id, email, refresh_token)
		VALUES ($1, $2, $3)
		ON CONFLICT (google_id) DO UPDATE SET email = $2, refresh_token = CASE WHEN $3 <> '' THEN $3 ELSE users.refresh_token END
		RETURNING id, platforms
	`, userInfo.ID, userInfo.Email, encryptedRefreshToken).Scan(&userID, &platforms)

	if err != nil {
		slog.Error("failed to upsert user", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	if h.Valkey != nil {
		cacheKey := models.UserCacheKey(userID)
		valKey := fmt.Sprintf("user:cal_val:%d", userID)
		if err := h.Valkey.Del(r.Context(), cacheKey, valKey).Err(); err != nil {
			slog.Error("failed to invalidate user cache after oauth upsert", "user_id", userID, "error", err)
		}
	}

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

	if len(platforms) > 0 {
		if err := h.Queue.PublishSyncTask(r.Context(), userID); err != nil {
			slog.Error("failed to queue initial sync", "user_id", userID, "error", err)
		}
	}

	http.Redirect(w, r, "/preferences", http.StatusSeeOther)
}

func (h *Handlers) ValidateCalendarAccess(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(ContextKeyUserID)
	if val == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	userID := val.(int)

	cacheKey := fmt.Sprintf("user:cal_val:%d", userID)
	if h.Valkey != nil {
		cachedVal, err := h.Valkey.Get(r.Context(), cacheKey).Result()
		if err == nil && cachedVal != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(cachedVal))
			return
		}
	}

	var encryptedRefreshToken string
	err := h.DB.QueryRow(r.Context(), "SELECT refresh_token FROM users WHERE id = $1", userID).Scan(&encryptedRefreshToken)
	if err != nil || encryptedRefreshToken == "" {
		res := map[string]any{
			"valid":      false,
			"code":       "credential_failure",
			"error_type": "missing_refresh_token",
		}
		writeJSONAndCache(w, r, h.Valkey, cacheKey, res, 5*time.Minute)
		return
	}

	refreshToken, err := auth.DecryptToken(encryptedRefreshToken, h.EncryptionKey)
	if err != nil || refreshToken == "" {
		res := map[string]any{
			"valid":      false,
			"code":       "credential_failure",
			"error_type": "invalid_refresh_token",
		}
		writeJSONAndCache(w, r, h.Valkey, cacheKey, res, 5*time.Minute)
		return
	}

	tokenSource := h.AuthProvider.Config.TokenSource(r.Context(), &oauth2.Token{RefreshToken: refreshToken})
	srv, err := calendar.NewService(r.Context(), option.WithTokenSource(tokenSource))
	if err != nil {
		res := map[string]any{
			"valid":      false,
			"code":       "operational_failure",
			"error_type": "calendar_service_error",
			"details":    err.Error(),
		}
		writeJSONAndCache(w, r, h.Valkey, cacheKey, res, 1*time.Minute)
		return
	}

	_, err = srv.Calendars.Get("primary").Do()
	if err != nil {
		code := "operational_failure"
		if gErr, ok := err.(*googleapi.Error); ok {
			if gErr.Code == http.StatusUnauthorized || gErr.Code == http.StatusForbidden {
				code = "credential_failure"
			}
		}
		res := map[string]any{
			"valid":      false,
			"code":       code,
			"error_type": "calendar_api_error",
			"details":    err.Error(),
		}
		ttl := 5 * time.Minute
		if code == "operational_failure" {
			ttl = 1 * time.Minute
		}
		writeJSONAndCache(w, r, h.Valkey, cacheKey, res, ttl)
		return
	}

	res := map[string]any{
		"valid": true,
		"code":  "success",
	}
	writeJSONAndCache(w, r, h.Valkey, cacheKey, res, 5*time.Minute)
}

func writeJSONAndCache(w http.ResponseWriter, r *http.Request, rdb *redis.Client, cacheKey string, payload map[string]any, ttl time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	data, _ := json.Marshal(payload)
	w.Write(data)
	if rdb != nil && cacheKey != "" {
		rdb.Set(r.Context(), cacheKey, string(data), ttl)
	}
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
		readPool := h.ReadDB
		if readPool == nil {
			readPool = h.DB
		}
		var calendarID sql.NullString
		var encryptedRefreshToken string
		err := readPool.QueryRow(r.Context(), "SELECT id, google_id, email, calendar_id, use_dedicated, platforms, refresh_token FROM users WHERE id = $1", userID).Scan(
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
		Platforms           []string `json:"platforms"`
		CSRFToken           string   `json:"csrf_token"`
		RefreshTokenMissing bool     `json:"refresh_token_missing"`
	}
	user.ID = cachedUser.ID
	user.GoogleID = cachedUser.GoogleID
	user.Email = cachedUser.Email
	user.CalendarID = cachedUser.CalendarID
	user.UseDedicated = cachedUser.UseDedicated
	user.Platforms = cachedUser.Platforms
	user.RefreshTokenMissing = cachedUser.RefreshToken == ""

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

	deleteGoogleData := r.URL.Query().Get("delete_google_data") == "true"

	var calendarID string
	var encryptedRefreshToken string
	err := h.DB.QueryRow(r.Context(), "SELECT COALESCE(calendar_id, ''), refresh_token FROM users WHERE id = $1", userID).Scan(&calendarID, &encryptedRefreshToken)

	var eventIDs []string
	if err == nil && deleteGoogleData {
		rows, qErr := h.DB.Query(r.Context(), "SELECT google_event_id FROM synced_events WHERE user_id = $1", userID)
		if qErr == nil {
			for rows.Next() {
				var eid string
				if scanErr := rows.Scan(&eid); scanErr == nil {
					eventIDs = append(eventIDs, eid)
				}
			}
			rows.Close()
		} else {
			slog.Error("failed to query synced events on account deletion", "user_id", userID, "error", qErr)
		}
	}

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
			return
		}
		slog.Error("failed to fetch user details on account deletion", "user_id", userID, "error", err)
	}

	if err == nil && encryptedRefreshToken != "" {
		refreshToken, decryptErr := auth.DecryptToken(encryptedRefreshToken, h.EncryptionKey)
		if decryptErr != nil {
			slog.Error("failed to decrypt refresh token on account deletion", "user_id", userID, "error", decryptErr)
		} else if refreshToken == "" {
			slog.Warn("decrypted refresh token is empty on account deletion", "user_id", userID)
		} else {
			h.CleanupWG.Add(1)
			go func() {
				defer h.CleanupWG.Done()
				defer func() {
					if r := recover(); r != nil {
						slog.Error("panic in background account cleanup", "user_id", userID, "recover", r, "stack", string(debug.Stack()))
					}
				}()
				timeout := time.Duration(len(eventIDs)) * 100 * time.Millisecond
				if timeout < 10*time.Second {
					timeout = 10 * time.Second
				}
				if timeout > 20*time.Second {
					timeout = 20 * time.Second
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				slog.Info("starting google cleanup in background", "user_id", userID, "delete_google_data", deleteGoogleData, "calendar_id", calendarID, "event_count", len(eventIDs))
				defer slog.Info("google cleanup finished in background", "user_id", userID)
				if deleteGoogleData {
					tokenSource := h.AuthProvider.Config.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
					srv, srvErr := calendar.NewService(ctx, option.WithTokenSource(tokenSource))
					if srvErr != nil {
						slog.Error("failed to create calendar service in background on account deletion", "user_id", userID, "error", srvErr)
					} else {
						if calendarID != "" && calendarID != "primary" {
							var delErr error
							for attempt := 1; attempt <= 3; attempt++ {
								delErr = srv.Calendars.Delete(calendarID).Context(ctx).Do()
								if delErr == nil {
									break
								}
								if gErr, ok := delErr.(*googleapi.Error); ok {
									if gErr.Code == http.StatusNotFound {
										break
									}
									if gErr.Code == http.StatusBadRequest || gErr.Code == http.StatusUnauthorized {
										break
									}
								}
								if attempt < 3 {
									time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
								}
							}
							if delErr != nil {
								if gErr, ok := delErr.(*googleapi.Error); ok && gErr.Code == http.StatusNotFound {
									slog.Info("custom calendar already deleted in background", "user_id", userID, "calendar_id", calendarID)
								} else {
									slog.Error("failed to delete custom calendar in background", "user_id", userID, "calendar_id", calendarID, "error", delErr)
								}
							} else {
								slog.Info("successfully deleted custom calendar in background", "user_id", userID, "calendar_id", calendarID)
							}
						} else if len(eventIDs) > 0 {
							toDelete := eventIDs
							if len(toDelete) > 200 {
								slog.Warn("large event batch on primary calendar deletion, capping to 200", "user_id", userID, "total_events", len(eventIDs))
								toDelete = toDelete[:200]
							}
							var wg sync.WaitGroup
							sem := make(chan struct{}, 20)
							badToken := false
							var mu sync.Mutex
							for _, eid := range toDelete {
								mu.Lock()
								if badToken {
									mu.Unlock()
									break
								}
								mu.Unlock()
								wg.Add(1)
								go func(id string) {
									defer wg.Done()
									select {
									case sem <- struct{}{}:
									case <-ctx.Done():
										return
									}
									defer func() { <-sem }()
									var delErr error
									for attempt := 1; attempt <= 3; attempt++ {
										delErr = srv.Events.Delete("primary", id).Context(ctx).Do()
										if delErr == nil {
											break
										}
										if gErr, ok := delErr.(*googleapi.Error); ok {
											if gErr.Code == http.StatusNotFound || gErr.Code == http.StatusGone {
												break
											}
											if gErr.Code == http.StatusBadRequest || gErr.Code == http.StatusUnauthorized {
												mu.Lock()
												badToken = true
												mu.Unlock()
												break
											}
										}
										if attempt < 3 {
											time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
										}
									}
									if delErr != nil {
										if gErr, ok := delErr.(*googleapi.Error); ok && (gErr.Code == http.StatusNotFound || gErr.Code == http.StatusGone) {
											slog.Info("primary calendar event already deleted in background", "user_id", userID, "event_id", id)
										} else {
											slog.Error("failed to delete primary calendar event in background", "user_id", userID, "event_id", id, "error", delErr)
										}
									}
								}(eid)
								time.Sleep(100 * time.Millisecond)
							}
							wg.Wait()
						} else {
							slog.Info("no custom calendar or primary events to delete", "user_id", userID)
						}
					}
				}
				resp, postErr := http.PostForm("https://oauth2.googleapis.com/revoke", url.Values{"token": {refreshToken}})
				if postErr != nil {
					slog.Error("failed to revoke oauth token in background", "user_id", userID, "error", postErr)
				} else {
					defer resp.Body.Close()
					_, _ = io.Copy(io.Discard, resp.Body)
					if resp.StatusCode != http.StatusOK {
						slog.Warn("oauth revoke returned non-200 status in background", "user_id", userID, "status", resp.StatusCode)
					} else {
						slog.Info("successfully revoked oauth token in background", "user_id", userID)
					}
				}
			}()
		}
	} else if err == nil && encryptedRefreshToken == "" {
		slog.Warn("refresh token is empty on account deletion", "user_id", userID)
	}

	_, delErr := h.DB.Exec(r.Context(), "DELETE FROM users WHERE id = $1", userID)
	if delErr != nil {
		slog.Error("failed to delete user account", "user_id", userID, "error", delErr)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	if h.Valkey != nil {
		cacheKey := models.UserCacheKey(userID)
		if err := h.Valkey.Del(r.Context(), cacheKey).Err(); err != nil {
			slog.Error("failed to invalidate user cache on account deletion", "user_id", userID, "error", err)
		}
	}

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

	cacheKey := models.PlatformsCacheKey()
	if h.Valkey != nil {
		val, err := h.Valkey.Get(r.Context(), cacheKey).Result()
		if err == nil {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(val))
			return
		}
	}

	payload := map[string][]string{"platforms": extractor.Platforms}
	serialized, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal platforms payload", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	if h.Valkey != nil {
		if err := h.Valkey.Set(r.Context(), cacheKey, string(serialized), models.PlatformsCacheTTL).Err(); err != nil {
			slog.Error("failed to write platforms cache", "error", err)
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write(serialized)
}

func (h *Handlers) SavePreferences(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(ContextKeyUserID).(int)
	var req struct {
		Platforms       []string `json:"platforms"`
		UseDedicated    bool     `json:"use_dedicated"`
		CaptchaResponse string   `json:"h-captcha-response"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	secret := os.Getenv("HCAPTCHA_SECRET")
	isDev := h.Env == "development" || h.Env == "dev" || h.Env == "local" || h.Env == ""
	if secret == "" && !isDev {
		http.Error(w, `{"error":"captcha configuration error"}`, http.StatusInternalServerError)
		return
	}
	if secret != "" || !isDev {
		if req.CaptchaResponse == "" {
			http.Error(w, `{"error":"captcha verification required"}`, http.StatusBadRequest)
			return
		}
		valid, err := verifyHCaptcha(r.Context(), req.CaptchaResponse)
		if err != nil || !valid {
			http.Error(w, `{"error":"captcha verification failed"}`, http.StatusForbidden)
			return
		}
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

	var currentPlatforms []string
	var currentUseDedicated bool
	cacheKey := models.UserCacheKey(userID)
	cacheFound := false

	if h.Valkey != nil {
		cachedVal, err := h.Valkey.Get(r.Context(), cacheKey).Result()
		if err == nil {
			var cachedUser models.CachedUser
			if err := json.Unmarshal([]byte(cachedVal), &cachedUser); err == nil {
				currentPlatforms = cachedUser.Platforms
				currentUseDedicated = cachedUser.UseDedicated
				cacheFound = true
			}
		}
	}

	if !cacheFound {
		readPool := h.ReadDB
		if readPool == nil {
			readPool = h.DB
		}
		var cachedUser models.CachedUser
		var calendarID sql.NullString
		var encryptedRefreshToken string
		err := readPool.QueryRow(r.Context(), "SELECT id, google_id, email, calendar_id, use_dedicated, platforms, refresh_token FROM users WHERE id = $1", userID).Scan(
			&cachedUser.ID, &cachedUser.GoogleID, &cachedUser.Email, &calendarID, &cachedUser.UseDedicated, &cachedUser.Platforms, &encryptedRefreshToken,
		)
		if err != nil {
			slog.Error("failed to fetch current preferences from read db", "user_id", userID, "error", err)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
		cachedUser.CalendarID = ""
		if calendarID.Valid {
			cachedUser.CalendarID = calendarID.String
		}
		cachedUser.RefreshToken = encryptedRefreshToken

		currentPlatforms = cachedUser.Platforms
		currentUseDedicated = cachedUser.UseDedicated

		if h.Valkey != nil {
			if serialized, err := json.Marshal(cachedUser); err == nil {
				if err := h.Valkey.Set(r.Context(), cacheKey, string(serialized), models.UserCacheTTL).Err(); err != nil {
					slog.Error("failed to write user cache in save preferences", "user_id", userID, "error", err)
				}
			}
		}
	}

	changed := false
	if len(req.Platforms) != len(currentPlatforms) || req.UseDedicated != currentUseDedicated {
		changed = true
	} else {
		counts := make(map[string]int)
		for _, p := range currentPlatforms {
			counts[p]++
		}
		for _, p := range req.Platforms {
			counts[p]--
		}
		for _, c := range counts {
			if c != 0 {
				changed = true
				break
			}
		}
	}

	if changed {
		var err error
		if req.UseDedicated {
			_, err = h.DB.Exec(r.Context(), "UPDATE users SET platforms = $1, use_dedicated = $2 WHERE id = $3", req.Platforms, req.UseDedicated, userID)
		} else {
			_, err = h.DB.Exec(r.Context(), "UPDATE users SET platforms = $1, use_dedicated = $2, calendar_id = NULL WHERE id = $3", req.Platforms, req.UseDedicated, userID)
		}
		if err != nil {
			slog.Error("failed to update user platforms in write db", "user_id", userID, "error", err)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}

		if h.Valkey != nil {
			if err := h.Valkey.Del(r.Context(), cacheKey).Err(); err != nil {
				slog.Error("failed to invalidate user cache on preference update", "user_id", userID, "error", err)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"changed": changed,
	})
}
func generateRandomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func verifyHCaptcha(ctx context.Context, responseToken string) (bool, error) {
	secret := os.Getenv("HCAPTCHA_SECRET")
	if secret == "" {
		env := os.Getenv("ENV")
		if env == "development" || env == "dev" || env == "local" || env == "" {
			return true, nil
		}
		return false, fmt.Errorf("hcaptcha secret is missing")
	}
	form := url.Values{
		"secret":   {secret},
		"response": {responseToken},
	}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://hcaptcha.com/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	var res struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return false, err
	}
	return res.Success, nil
}
