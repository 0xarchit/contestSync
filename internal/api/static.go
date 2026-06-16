package api

import (
	"io/fs"
	"net/http"
	"strings"
)

type StaticServer struct {
	fs      fs.FS
	handler http.Handler
}

func NewStaticServer(staticFS fs.FS) (http.Handler, error) {
	return &StaticServer{
		fs:      staticFS,
		handler: http.FileServer(http.FS(staticFS)),
	}, nil
}

func (s *StaticServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		s.handler.ServeHTTP(w, r)
		return
	}

	_, err := s.fs.Open(path)
	if err == nil {
		s.handler.ServeHTTP(w, r)
		return
	}

	if !strings.Contains(path, ".") {
		htmlPath := path + ".html"
		_, err := s.fs.Open(htmlPath)
		if err == nil {
			r.URL.Path = "/" + htmlPath
			s.handler.ServeHTTP(w, r)
			return
		}
	}

	s.handler.ServeHTTP(w, r)
}
