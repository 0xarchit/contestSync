package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
)

type StaticFile struct {
	Content     []byte
	ContentType string
	ETag        string
}

type MemoryFS struct {
	files map[string]StaticFile
}

func NewStaticServer(staticFS fs.FS) (http.Handler, error) {
	files := make(map[string]StaticFile)
	err := fs.WalkDir(staticFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := staticFS.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		content, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		cleanPath := "/" + filepath.ToSlash(path)
		ext := strings.ToLower(filepath.Ext(path))
		var minified []byte
		switch ext {
		case ".css":
			minified = []byte(MinifyCSS(string(content)))
		case ".js":
			minified = []byte(MinifyJS(string(content)))
		case ".html", ".htm":
			minified = []byte(MinifyHTML(string(content)))
		default:
			minified = content
		}
		h := sha256.New()
		h.Write(minified)
		etag := `"` + hex.EncodeToString(h.Sum(nil)) + `"`
		files[cleanPath] = StaticFile{
			Content:     minified,
			ContentType: getContentType(path, minified),
			ETag:        etag,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return &MemoryFS{files: files}, nil
}

func (m *MemoryFS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" || strings.HasSuffix(path, "/") {
		path = filepath.Join(path, "index.html")
	}
	path = filepath.ToSlash(filepath.Clean(path))
	file, ok := m.files[path]
	if !ok {
		if !strings.HasSuffix(path, ".html") {
			file, ok = m.files[path+".html"]
		}
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("If-None-Match") == file.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("ETag", file.ETag)
	w.Header().Set("Cache-Control", "public, max-age=31536000, must-revalidate")
	w.Write(file.Content)
}

func getContentType(path string, content []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".webp":
		return "image/webp"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	default:
		ct := http.DetectContentType(content)
		if ct == "" {
			return "application/octet-stream"
		}
		return ct
	}
}

func MinifyCSS(src string) string {
	var buf bytes.Buffer
	inComment := false
	inString := false
	var stringChar rune
	runes := []rune(src)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := runes[i]
		if inComment {
			if r == '*' && i+1 < n && runes[i+1] == '/' {
				inComment = false
				i++
			}
			continue
		}
		if inString {
			buf.WriteRune(r)
			if r == stringChar && runes[i-1] != '\\' {
				inString = false
			}
			continue
		}
		if r == '/' && i+1 < n && runes[i+1] == '*' {
			inComment = true
			i++
			continue
		}
		if r == '"' || r == '\'' || r == '`' {
			inString = true
			stringChar = r
			buf.WriteRune(r)
			continue
		}
		if unicode.IsSpace(r) {
			if buf.Len() > 0 {
				last := rune(buf.Bytes()[buf.Len()-1])
				if isCSSWordChar(last) && i+1 < n && isCSSWordChar(runes[i+1]) {
					buf.WriteRune(' ')
				}
			}
			continue
		}
		buf.WriteRune(r)
	}
	return buf.String()
}

func isCSSWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '%'
}

func MinifyJS(src string) string {
	var buf bytes.Buffer
	inBlockComment := false
	inLineComment := false
	inString := false
	var stringChar rune
	runes := []rune(src)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := runes[i]
		if inBlockComment {
			if r == '*' && i+1 < n && runes[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inLineComment {
			if r == '\n' || r == '\r' {
				inLineComment = false
				buf.WriteRune('\n')
			}
			continue
		}
		if inString {
			buf.WriteRune(r)
			if r == stringChar && runes[i-1] != '\\' {
				inString = false
			}
			continue
		}
		if r == '/' && i+1 < n && runes[i+1] == '*' {
			inBlockComment = true
			i++
			continue
		}
		if r == '/' && i+1 < n && runes[i+1] == '/' {
			inLineComment = true
			i++
			continue
		}
		if r == '"' || r == '\'' || r == '`' {
			inString = true
			stringChar = r
			buf.WriteRune(r)
			continue
		}
		if unicode.IsSpace(r) {
			if r == '\n' || r == '\r' {
				if buf.Len() > 0 {
					last := rune(buf.Bytes()[buf.Len()-1])
					if last != '\n' && last != '\r' {
						buf.WriteRune('\n')
					}
				}
				continue
			}
			if buf.Len() > 0 {
				last := rune(buf.Bytes()[buf.Len()-1])
				if isJSWordChar(last) && i+1 < n && isJSWordChar(runes[i+1]) {
					buf.WriteRune(' ')
				}
			}
			continue
		}
		buf.WriteRune(r)
	}
	lines := strings.Split(buf.String(), "\n")
	var result bytes.Buffer
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		result.WriteString(trimmed)
		result.WriteRune('\n')
	}
	return result.String()
}

func isJSWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$'
}

func MinifyHTML(src string) string {
	var buf bytes.Buffer
	inComment := false
	runes := []rune(src)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := runes[i]
		if inComment {
			if r == '-' && i+2 < n && runes[i+1] == '-' && runes[i+2] == '>' {
				inComment = false
				i += 2
			}
			continue
		}
		if r == '<' && i+3 < n && runes[i+1] == '!' && runes[i+2] == '-' && runes[i+3] == '-' {
			inComment = true
			i += 3
			continue
		}
		if unicode.IsSpace(r) {
			if buf.Len() > 0 {
				last := rune(buf.Bytes()[buf.Len()-1])
				if !unicode.IsSpace(last) {
					buf.WriteRune(' ')
				}
			}
			continue
		}
		buf.WriteRune(r)
	}
	return buf.String()
}
