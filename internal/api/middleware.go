package api

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
)

type rateLimiter struct {
	sync.Mutex
	ips map[string][]time.Time
}

func (rl *rateLimiter) limit(ip string, max int, duration time.Duration) bool {
	rl.Lock()
	defer rl.Unlock()

	now := time.Now()
	if rl.ips == nil {
		rl.ips = make(map[string][]time.Time)
	}

	times := rl.ips[ip]
	var filtered []time.Time
	for _, t := range times {
		if now.Sub(t) < duration {
			filtered = append(filtered, t)
		}
	}

	if len(filtered) >= max {
		rl.ips[ip] = filtered
		return false
	}

	rl.ips[ip] = append(filtered, now)
	return true
}

var globalLimiter = &rateLimiter{}

func RateLimitMiddleware(max int, duration time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr // Simplistic IP extraction
			if !globalLimiter.limit(ip, max, duration) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type ContextKey string

const (
	ContextKeyUserID    ContextKey = "user_id"
	ContextKeyRequestID ContextKey = "request_id"
)

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		w.Header().Set("X-Request-Id", requestID)
		ctx := context.WithValue(r.Context(), ContextKeyRequestID, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func SecurityHeadersMiddleware(env string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://cdnjs.cloudflare.com https://unpkg.com; style-src 'self' https://fonts.googleapis.com https://unpkg.com 'unsafe-inline'; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self';")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-XSS-Protection", "0")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

			if env == "production" {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
			}

			next.ServeHTTP(w, r)
		})
	}
}

func RequestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start),
			"ip", r.RemoteAddr,
		)
	})
}

func RequireAuth(store *sessions.CookieStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := store.Get(r, "session")
			if err != nil {
				session.Options.MaxAge = -1
				session.Save(r, w)
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			userID, ok := session.Values["user_id"].(int)
			if !ok || userID == 0 {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), ContextKeyUserID, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func CSRFMiddleware(store *sessions.CookieStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, _ := store.Get(r, "session")
			expectedToken, ok := session.Values["csrf_token"].(string)
			if !ok || expectedToken == "" {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			actualToken := r.Header.Get("X-CSRF-Token")
			if subtle.ConstantTimeCompare([]byte(expectedToken), []byte(actualToken)) != 1 {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
