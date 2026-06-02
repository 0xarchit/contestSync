package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/sessions"
)

func TestRequestIDMiddleware(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Context().Value(ContextKeyRequestID)
		if reqID == nil || reqID.(string) == "" {
			t.Error("expected request_id in context, got nil/empty")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("expected X-Request-Id header to be set, got empty")
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	middleware := SecurityHeadersMiddleware("production")
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected X-Content-Type-Options to be nosniff, got %q", resp.Header.Get("X-Content-Type-Options"))
	}
	if resp.Header.Get("Strict-Transport-Security") == "" {
		t.Error("expected STS header in production, got empty")
	}
}

func TestRequireAuth(t *testing.T) {
	store := sessions.NewCookieStore([]byte("testsecret"))
	middleware := RequireAuth(store)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Context().Value(ContextKeyUserID).(int)
		if uid != 123 {
			t.Errorf("expected userID 123, got %d", uid)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	session, _ := store.Get(req, "session")
	session.Values["user_id"] = 123

	w2 := httptest.NewRecorder()
	session.Save(req, w2)

	for k, v := range w2.Header() {
		req.Header[k] = v
	}

	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected StatusOK, got %d", w.Result().StatusCode)
	}
}

func TestRequireAuthUnauthorized(t *testing.T) {
	store := sessions.NewCookieStore([]byte("testsecret"))
	middleware := RequireAuth(store)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusUnauthorized {
		t.Errorf("expected StatusUnauthorized, got %d", w.Result().StatusCode)
	}
}

func TestCSRFMiddleware(t *testing.T) {
	store := sessions.NewCookieStore([]byte("testsecret"))
	middleware := CSRFMiddleware(store)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("X-CSRF-Token", "valid-token")

	session, _ := store.Get(req, "session")
	session.Values["csrf_token"] = "valid-token"

	w2 := httptest.NewRecorder()
	session.Save(req, w2)
	for k, v := range w2.Header() {
		req.Header[k] = v
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected StatusOK, got %d", w.Result().StatusCode)
	}
}

func TestCSRFMiddlewareForbidden(t *testing.T) {
	store := sessions.NewCookieStore([]byte("testsecret"))
	middleware := CSRFMiddleware(store)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("X-CSRF-Token", "invalid-token")

	session, _ := store.Get(req, "session")
	session.Values["csrf_token"] = "valid-token"

	w2 := httptest.NewRecorder()
	session.Save(req, w2)
	for k, v := range w2.Header() {
		req.Header[k] = v
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusForbidden {
		t.Errorf("expected StatusForbidden, got %d", w.Result().StatusCode)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	middleware := RateLimitMiddleware(nil, 2, 1*time.Second)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "127.0.0.1:1234"

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req)
	if w1.Result().StatusCode != http.StatusOK {
		t.Errorf("expected StatusOK on first request, got %d", w1.Result().StatusCode)
	}

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req)
	if w2.Result().StatusCode != http.StatusOK {
		t.Errorf("expected StatusOK on second request, got %d", w2.Result().StatusCode)
	}

	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req)
	if w3.Result().StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected StatusTooManyRequests on third request, got %d", w3.Result().StatusCode)
	}
}
