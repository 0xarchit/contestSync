package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"
)

func TestStaticServer(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mockFS := fstest.MapFS{
		"style.css": &fstest.MapFile{
			Data:    []byte("body { margin: 0; }"),
			ModTime: now,
		},
		"index.html": &fstest.MapFile{
			Data:    []byte("<h1>Hello</h1>"),
			ModTime: now,
		},
	}
	server, err := NewStaticServer(mockFS)
	if err != nil {
		t.Fatalf("failed to create static server: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/style.css", nil)
	server.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") == "" {
		t.Error("expected Content-Type header to be set")
	}
	lastMod := resp.Header.Get("Last-Modified")
	if lastMod == "" {
		t.Error("expected Last-Modified header to be set")
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/style.css", nil)
	req2.Header.Set("If-Modified-Since", lastMod)
	server.ServeHTTP(w2, req2)
	resp2 := w2.Result()
	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("expected 304, got %d", resp2.StatusCode)
	}
}
