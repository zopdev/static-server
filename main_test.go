package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServer(t *testing.T) {
	// Create a temporary directory
	//nolint:staticcheck // Ignore as we are testing the server
	tempDir := os.TempDir()

	// Create an "index" directory
	indexDirPath := filepath.Join(tempDir, "docs")
	if err := os.MkdirAll(indexDirPath, 0600); err != nil {
		t.Fatalf("Failed to create directory %s: %v", indexDirPath, err)
	}

	// Create necessary files
	files := []struct {
		name    string
		content string
	}{
		{"/index.html", "<html><body>Index</body></html>"},
		{"/404.html", "<html><body>404 Not Found</body></html>"},
		{"/docs.html", "<html><body>Index</body></html>"},
	}

	for _, file := range files {
		filePath := filepath.Join(tempDir, file.name)
		if err := os.MkdirAll(filepath.Dir(filePath), 0600); err != nil {
			t.Fatalf("Failed to create dir for file %s: %v", file.name, err)
		}

		if err := os.WriteFile(filePath, []byte(file.content), 0600); err != nil {
			t.Fatalf("Failed to write file %s: %v", file.name, err)
		}
	}

	// Set the environment variable for the static file path
	t.Setenv("STATIC_DIR_PATH", tempDir)

	go main()

	time.Sleep(3 * time.Second)

	tests := []struct {
		path       string
		statusCode int
	}{
		{"/", http.StatusOK},
		{"/docs", http.StatusOK},
		{"/index", http.StatusOK},
		{"/index/", http.StatusOK},
		{tempDir + "/index.html", http.StatusNotFound},
		{"/index.html", http.StatusOK},
		{"/nonexistent", http.StatusNotFound},
	}

	for _, test := range tests {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost:8000"+test.path, http.NoBody)
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}

		client := &http.Client{}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to perform request: %v", err)
		}

		if resp.StatusCode != test.statusCode {
			t.Errorf("Expected status code %v, got %v for path %v", test.statusCode, resp.StatusCode, test.path)
		}

		_ = resp.Body.Close()
	}

	_ = os.RemoveAll(tempDir) //nolint:staticcheck // Intentionally removing test temp directory
}

func TestSanitizePath(t *testing.T) {
	// Create a temporary directory to use as static path
	staticDir, err := os.MkdirTemp("", "static-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	defer func() {
		_ = os.RemoveAll(staticDir)
	}()

	tests := []struct {
		name         string
		requestPath  string
		shouldPass   bool
		expectedPath string // expected suffix after staticDir
	}{
		// Valid paths - should pass and return correct path
		{"root path", "/", true, "/"},
		{"simple file", "/index.html", true, "/index.html"},
		{"nested path", "/docs/readme.md", true, "/docs/readme.md"},
		{"path with dots in filename", "/file.name.txt", true, "/file.name.txt"},

		// Path traversal attempts - should be neutralized to safe paths
		// The filepath.Clean normalizes these before joining, keeping them within staticDir
		{"parent directory normalized", "/..", true, "/"},
		{"parent with slash normalized", "/../", true, "/"},
		{"traverse attempt normalized", "/../etc/passwd", true, "/etc/passwd"},
		{"multiple traversal normalized", "/../../../etc/passwd", true, "/etc/passwd"},
		{"traverse from subdir normalized", "/docs/../../../etc/passwd", true, "/etc/passwd"},
		{"mixed traversal normalized", "/docs/../../secret", true, "/secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := sanitizePath(staticDir, tt.requestPath)
			if ok != tt.shouldPass {
				t.Errorf("sanitizePath(%q, %q) ok = %v, want %v", staticDir, tt.requestPath, ok, tt.shouldPass)
			}

			if ok && tt.expectedPath != "" {
				expectedFull := filepath.Join(staticDir, tt.expectedPath)
				if result != expectedFull {
					t.Errorf("sanitizePath(%q, %q) = %q, want %q", staticDir, tt.requestPath, result, expectedFull)
				}
			}
		})
	}
}

func TestSanitizePathPreventsEscape(t *testing.T) {
	// This test verifies that the sanitized path is ALWAYS within the static directory
	staticDir, err := os.MkdirTemp("", "static-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	defer func() {
		_ = os.RemoveAll(staticDir)
	}()

	absStaticDir, _ := filepath.Abs(staticDir)

	// Various attack vectors that could potentially escape the directory
	attackPaths := []string{
		"/../../../etc/passwd",
		"/..\\..\\..\\etc\\passwd",
		"/../" + staticDir + "/../etc/passwd",
		"/./../../etc/passwd",
		"/%2e%2e/%2e%2e/etc/passwd",
		"/docs/../../../../../../../etc/passwd",
	}

	for _, attackPath := range attackPaths {
		t.Run(attackPath, func(t *testing.T) {
			result, ok := sanitizePath(staticDir, attackPath)
			if !ok {
				// If it failed validation, that's also acceptable
				return
			}

			// If it passed, verify the result is within staticDir
			absResult, _ := filepath.Abs(result)
			if len(absResult) < len(absStaticDir) || absResult[:len(absStaticDir)] != absStaticDir {
				t.Errorf("Path escaped static directory: sanitizePath(%q, %q) = %q (not within %q)",
					staticDir, attackPath, absResult, absStaticDir)
			}
		})
	}
}
