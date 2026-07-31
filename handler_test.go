package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"gofr.dev/pkg/gofr/datasource/file"
	"gofr.dev/pkg/gofr/logging"
)

func setupTestDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	files := map[string]string{
		"index.html":      "<html>index</html>",
		"404.html":        "<html>404</html>",
		"docs.html":       "<html>docs</html>",
		"style.css":       "body{}",
		"blog/index.html": "<html>blog</html>",
		"data.json":       `{"key":"value"}`,
	}

	for name, content := range files {
		path := filepath.Join(dir, name)

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("Failed to create dir for %s: %v", name, err)
		}

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write %s: %v", name, err)
		}
	}

	return dir
}

func TestResolveFilePath(t *testing.T) {
	dir := setupTestDir(t)
	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))

	tests := []struct {
		name             string
		urlPath          string
		defaultExtension string
		wantPath         string
		wantHasExt       bool
	}{
		{"root path", rootPath, ".html", filepath.Join(dir, indexHTML), false},
		{"auto html extension", "/docs", ".html", filepath.Join(dir, "docs.html"), false},
		{"auto json extension", "/data", ".json", filepath.Join(dir, "data.json"), false},
		{"directory index", "/blog", ".html", filepath.Join(dir, "blog"+indexHTML), false},
		{"explicit extension", "/style.css", ".html", filepath.Join(dir, "style.css"), true},
		{"missing without extension", "/nonexistent", ".html", filepath.Join(dir, "nonexistent"), false},
		{"missing with extension", "/missing.js", ".html", filepath.Join(dir, "missing.js"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &staticFileHandler{fs: fs, staticFilePath: dir, defaultExtension: tt.defaultExtension}

			path, hasExt := h.resolveFilePath(tt.urlPath, false)

			assert.Equal(t, tt.wantPath, path)
			assert.Equal(t, tt.wantHasExt, hasExt)
		})
	}
}

func TestServeHTTP(t *testing.T) {
	dir := setupTestDir(t)
	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))

	tests := []struct {
		name       string
		urlPath    string
		spaMode    bool
		wantStatus int
	}{
		{"existing file", "/style.css", false, http.StatusOK},
		{"missing file", "/nonexistent", false, http.StatusNotFound},
		{"spa fallback", "/dashboard", true, http.StatusOK},
		{"spa missing with extension", "/missing.css", true, http.StatusNotFound},
		{"well-known passes through", "/.well-known/acme", false, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &staticFileHandler{
				fs:               fs,
				staticFilePath:   dir,
				spaMode:          tt.spaMode,
				defaultExtension: ".html",
				next:             http.NotFoundHandler(),
			}

			req := httptest.NewRequest(http.MethodGet, tt.urlPath, http.NoBody)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}
