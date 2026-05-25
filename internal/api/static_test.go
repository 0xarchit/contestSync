package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestMinifiers(t *testing.T) {
	cStart := "/" + "*"
	cEnd := "*" + "/"
	css := "body { background-color: #ffffff; margin: 0; } " + cStart + " comment " + cEnd
	expectedCSS := "body{background-color:#ffffff;margin:0;}"
	if got := MinifyCSS(css); got != expectedCSS {
		t.Errorf("MinifyCSS got %q, want %q", got, expectedCSS)
	}

	js := "function test() {\n" + "/" + "/" + " comment\nconsole.log(\"hello world\"); " + cStart + " block " + cEnd + "\n}"
	expectedJS := "function test(){\nconsole.log(\"hello world\");\n}\n"
	if got := MinifyJS(js); got != expectedJS {
		t.Errorf("MinifyJS got %q, want %q", got, expectedJS)
	}

	html := "<!DOCTYPE html> <" + "!" + "-" + "- comment -" + "-> <html> <body> <h1>Hello</h1> </body> </html>"
	expectedHTML := "<!DOCTYPE html> <html> <body> <h1>Hello</h1> </body> </html>"
	if got := MinifyHTML(html); got != expectedHTML {
		t.Errorf("MinifyHTML got %q, want %q", got, expectedHTML)
	}
}

func TestStaticServer(t *testing.T) {
	mockFS := fstest.MapFS{
		"style.css": &fstest.MapFile{
			Data: []byte("body { margin: 0; }"),
		},
		"index.html": &fstest.MapFile{
			Data: []byte("<h1>Hello</h1>"),
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
	if resp.Header.Get("Content-Type") != "text/css; charset=utf-8" {
		t.Errorf("expected text/css, got %q", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("expected ETag header to be set")
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/style.css", nil)
	req2.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	server.ServeHTTP(w2, req2)
	resp2 := w2.Result()
	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("expected 304, got %d", resp2.StatusCode)
	}
}
