package api

import (
	"container/list"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"github.com/redis/go-redis/v9"
)

type lruItem struct {
	key   string
	value []time.Time
}

type rateLimiter struct {
	sync.Mutex
	cap   int
	evict *list.List
	cache map[string]*list.Element
}

func (rl *rateLimiter) limit(ip string, max int, duration time.Duration) bool {
	rl.Lock()
	defer rl.Unlock()

	now := time.Now()
	if rl.cache == nil {
		rl.cache = make(map[string]*list.Element)
		rl.evict = list.New()
		if rl.cap == 0 {
			rl.cap = 10000
		}
	}

	var item *lruItem
	if elem, ok := rl.cache[ip]; ok {
		rl.evict.MoveToFront(elem)
		item = elem.Value.(*lruItem)
	} else {
		if rl.evict.Len() >= rl.cap {
			back := rl.evict.Back()
			if back != nil {
				rl.evict.Remove(back)
				backItem := back.Value.(*lruItem)
				delete(rl.cache, backItem.key)
			}
		}
		item = &lruItem{key: ip, value: []time.Time{}}
		elem := rl.evict.PushFront(item)
		rl.cache[ip] = elem
	}

	var filtered []time.Time
	for _, t := range item.value {
		if now.Sub(t) < duration {
			filtered = append(filtered, t)
		}
	}

	if len(filtered) >= max {
		item.value = filtered
		return false
	}

	item.value = append(filtered, now)
	return true
}

type VelocityCounter struct {
	hits      int64
	resetTime time.Time
}

type EarlyRateLimiter struct {
	mu       sync.RWMutex
	counters map[string]*VelocityCounter
}

func NewEarlyRateLimiter() *EarlyRateLimiter {
	return &EarlyRateLimiter{
		counters: make(map[string]*VelocityCounter),
	}
}

func (el *EarlyRateLimiter) Allow(ip string, limit int64, duration time.Duration) bool {
	el.mu.RLock()
	counter, ok := el.counters[ip]
	el.mu.RUnlock()
	now := time.Now()
	if !ok {
		el.mu.Lock()
		counter, ok = el.counters[ip]
		if !ok {
			counter = &VelocityCounter{
				hits:      1,
				resetTime: now.Add(duration),
			}
			el.counters[ip] = counter
			el.mu.Unlock()
			return true
		}
		el.mu.Unlock()
	}
	if now.After(counter.resetTime) {
		el.mu.Lock()
		if now.After(counter.resetTime) {
			counter.hits = 1
			counter.resetTime = now.Add(duration)
		} else {
			atomic.AddInt64(&counter.hits, 1)
		}
		el.mu.Unlock()
		return true
	}
	val := atomic.AddInt64(&counter.hits, 1)
	return val <= limit
}

var earlyLimiter = NewEarlyRateLimiter()
var globalLimiter = &rateLimiter{cap: 10000}

func getClientIP(r *http.Request) string {
	if os.Getenv("TRUST_PROXY") == "true" {
		if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
			return ip
		}
		if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
			if comma := strings.Index(ip, ","); comma != -1 {
				return strings.TrimSpace(ip[:comma])
			}
			return ip
		}
		if ip := r.Header.Get("X-Real-IP"); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func RateLimitMiddleware(valkeyClient *redis.Client, max int, duration time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getClientIP(r)

			retryAfter := strconv.Itoa(int(duration.Seconds()))

			if !earlyLimiter.Allow(ip, 150, 10*time.Second) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
				return
			}

			if valkeyClient != nil {
				key := "ratelimit:" + ip
				ctx := r.Context()
				pipe := valkeyClient.TxPipeline()
				incr := pipe.Incr(ctx, key)
				pipe.Expire(ctx, key, duration)
				_, err := pipe.Exec(ctx)
				if err == nil {
					if incr.Val() > int64(max) {
						w.Header().Set("Retry-After", retryAfter)
						http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
						return
					}
				} else {
					slog.Error("valkey rate limiter error, falling back to local rate limiter", "error", err)
					if !globalLimiter.limit(ip, max, duration) {
						w.Header().Set("Retry-After", retryAfter)
						http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
						return
					}
				}
			} else {
				if !globalLimiter.limit(ip, max, duration) {
					w.Header().Set("Retry-After", retryAfter)
					http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
					return
				}
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
		logger := slog.With("request_id", requestID)
		logger.Info("request started", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func SecurityHeadersMiddleware(env string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://cdnjs.cloudflare.com https://unpkg.com https://hcaptcha.com https://*.hcaptcha.com; style-src 'self' https://fonts.googleapis.com https://unpkg.com https://hcaptcha.com https://*.hcaptcha.com 'unsafe-inline'; font-src 'self' https://fonts.gstatic.com data: https://hcaptcha.com https://*.hcaptcha.com; img-src 'self' data: https: https://hcaptcha.com https://*.hcaptcha.com; connect-src 'self' https://api.github.com https://unpkg.com https://hcaptcha.com https://*.hcaptcha.com; frame-src 'self' https://hcaptcha.com https://*.hcaptcha.com; frame-ancestors 'none'; base-uri 'self'; form-action 'self';")
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
			"ip", getClientIP(r),
		)
	})
}

func RequireAuth(store sessions.Store) func(http.Handler) http.Handler {
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

func CSRFMiddleware(store sessions.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, _ := store.Get(r, "session")
			expectedToken, ok := session.Values["csrf_token"].(string)
			if !ok || expectedToken == "" {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			actualToken := r.Header.Get("X-CSRF-Token")
			if len(expectedToken) != len(actualToken) || subtle.ConstantTimeCompare([]byte(expectedToken), []byte(actualToken)) != 1 {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type ValkeyStore struct {
	client  *redis.Client
	codecs  []securecookie.Codec
	options *sessions.Options
}

func NewValkeyStore(client *redis.Client, env string, keyPairs ...[]byte) *ValkeyStore {
	return &ValkeyStore{
		client: client,
		codecs: securecookie.CodecsFromPairs(keyPairs...),
		options: &sessions.Options{
			Path:     "/",
			MaxAge:   86400 * 7,
			HttpOnly: true,
			Secure:   env != "development" && env != "dev" && env != "local",
			SameSite: http.SameSiteLaxMode,
		},
	}
}

func (s *ValkeyStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.GetRegistry(r).Get(s, name)
}

func (s *ValkeyStore) New(r *http.Request, name string) (*sessions.Session, error) {
	session := sessions.NewSession(s, name)
	opts := *s.options
	session.Options = &opts
	session.IsNew = true

	c, err := r.Cookie(name)
	if err != nil {
		return session, nil
	}

	var sessionID string
	err = securecookie.DecodeMulti(name, c.Value, &sessionID, s.codecs...)
	if err != nil {
		return session, nil
	}

	session.ID = sessionID
	val, err := s.client.Get(r.Context(), "session:"+sessionID).Result()
	if err != nil {
		return session, nil
	}

	var tempValues map[string]interface{}
	d := json.NewDecoder(strings.NewReader(val))
	d.UseNumber()
	if err := d.Decode(&tempValues); err != nil {
		return session, nil
	}

	values := make(map[interface{}]interface{})
	for k, v := range tempValues {
		if jn, ok := v.(json.Number); ok {
			if i64, err := jn.Int64(); err == nil {
				values[k] = int(i64)
			} else {
				values[k] = v
			}
		} else {
			values[k] = v
		}
	}

	session.Values = values
	session.IsNew = false
	return session, nil
}

func (s *ValkeyStore) Save(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {
	if session.Options.MaxAge < 0 {
		if session.ID != "" {
			if err := s.client.Del(r.Context(), "session:"+session.ID).Err(); err != nil {
				slog.Error("failed to delete session from valkey", "session_id", session.ID, "error", err)
			}
			session.ID = ""
		}
		cookie := sessions.NewCookie(session.Name(), "", session.Options)
		if session.Options.Secure {
			cookie.Secure = true
		}
		http.SetCookie(w, cookie)
		return nil
	}

	if session.ID == "" {
		session.ID = hex.EncodeToString(securecookie.GenerateRandomKey(32))
	}

	tempValues := make(map[string]interface{})
	for k, v := range session.Values {
		if sk, ok := k.(string); ok {
			tempValues[sk] = v
		}
	}

	val, err := json.Marshal(tempValues)
	if err != nil {
		return err
	}

	age := session.Options.MaxAge
	if age == 0 {
		age = s.options.MaxAge
	}

	err = s.client.Set(r.Context(), "session:"+session.ID, val, time.Duration(age)*time.Second).Err()
	if err != nil {
		return err
	}

	encoded, err := securecookie.EncodeMulti(session.Name(), session.ID, s.codecs...)
	if err != nil {
		return err
	}

	cookie := sessions.NewCookie(session.Name(), encoded, session.Options)
	if session.Options.Secure {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
	return nil
}
