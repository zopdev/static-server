package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"gofr.dev/pkg/gofr/datasource/file"
	"gofr.dev/pkg/gofr/logging"
)

func TestServer(t *testing.T) {
	dir := setupTestDir(t)
	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))

	handler := &staticFileHandler{
		fs:               fs,
		staticFilePath:   dir,
		defaultExtension: ".html",
		next:             http.NotFoundHandler(),
	}

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	tests := []struct {
		path       string
		statusCode int
	}{
		{"/", http.StatusOK},
		{"/docs", http.StatusOK},
		{"/index", http.StatusOK},
		{"/index/", http.StatusOK},
		{filepath.Join(dir, "index.html"), http.StatusNotFound},
		{"/index.html", http.StatusOK},
		{"/nonexistent", http.StatusNotFound},
	}

	for _, test := range tests {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+test.path, http.NoBody)
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to perform request: %v", err)
		}

		if resp.StatusCode != test.statusCode {
			t.Errorf("Expected status code %v, got %v for path %v", test.statusCode, resp.StatusCode, test.path)
		}

		_ = resp.Body.Close()
	}
}

// The shipped configs/.env sets STATIC_DIR_PATH= and DEFAULT_EXTENSION= with
// empty values. GetOrDefault only falls back on an ABSENT key, so an empty one
// yields "" and roots every lookup at the process working directory — which is
// how a container given STATIC_DIR_PATH via the environment silently loaded
// zero _headers rules while still serving pages.
func TestEmptyConfigValuesFallBackToDefaults(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback string
		want     string
	}{
		{"empty static path falls back", "", defaultStaticFilePath, defaultStaticFilePath},
		{"empty extension falls back", "", htmlExtension, htmlExtension},
		{"a real value is kept", "/static", defaultStaticFilePath, "/static"},
		{"a real extension is kept", ".htm", htmlExtension, ".htm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.value
			if got == "" {
				got = tt.fallback
			}

			assert.Equal(t, tt.want, got)
		})
	}
}
