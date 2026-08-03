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
// fakeConfig stands in for gofr's config: a key it holds is "present" even when
// its value is empty, which is the whole distinction resolveOrDefault exists to
// handle and the one the shipped configs/.env actually trips.
type fakeConfig map[string]string

func (f fakeConfig) GetOrDefault(key, fallback string) string {
	if value, ok := f[key]; ok {
		return value
	}

	return fallback
}

func TestResolveOrDefault(t *testing.T) {
	tests := []struct {
		name     string
		cfg      fakeConfig
		key      string
		fallback string
		want     string
	}{
		// The regression: configs/.env ships STATIC_DIR_PATH= and
		// DEFAULT_EXTENSION= with empty values, so GetOrDefault finds the key,
		// returns "", and every path lookup roots at the working directory.
		{"present but empty falls back", fakeConfig{"STATIC_DIR_PATH": ""}, "STATIC_DIR_PATH", defaultStaticFilePath, defaultStaticFilePath},
		{"present but empty extension falls back", fakeConfig{"DEFAULT_EXTENSION": ""}, "DEFAULT_EXTENSION", htmlExtension, htmlExtension},

		{"absent falls back", fakeConfig{}, "STATIC_DIR_PATH", defaultStaticFilePath, defaultStaticFilePath},
		{"a real value is kept", fakeConfig{"STATIC_DIR_PATH": "/srv/site"}, "STATIC_DIR_PATH", defaultStaticFilePath, "/srv/site"},
		{"a real extension is kept", fakeConfig{"DEFAULT_EXTENSION": ".htm"}, "DEFAULT_EXTENSION", htmlExtension, ".htm"},

		// Whitespace is a value, not emptiness — guessing at it would be a
		// different bug from the one being fixed.
		{"whitespace is kept as given", fakeConfig{"DEFAULT_EXTENSION": " "}, "DEFAULT_EXTENSION", htmlExtension, " "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveOrDefault(tt.cfg, tt.key, tt.fallback))
		})
	}
}
