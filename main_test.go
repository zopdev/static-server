package main

import (
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServer(t *testing.T) {
	// Create a temporary directory
	tempDir, err := ioutil.TempDir("", "static")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create necessary files
	files := []struct {
		name    string
		content string
	}{
		{"/index.html", "<html><body>Index</body></html>"},
		{"/404.html", "<html><body>404 Not Found</body></html>"},
	}

	for _, file := range files {
		filePath := filepath.Join(tempDir, file.name)
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			t.Fatalf("Failed to create dir for file %s: %v", file.name, err)
		}
		if err := ioutil.WriteFile(filePath, []byte(file.content), 0644); err != nil {
			t.Fatalf("Failed to write file %s: %v", file.name, err)
		}
	}

	// Set the environment variable for the static file path
	t.Setenv("STATIC_FILE_PATH", tempDir)

	go main()

	time.Sleep(3 * time.Second)

	tests := []struct {
		path       string
		statusCode int
	}{
		{"/", http.StatusOK},
		{"/index", http.StatusOK},
		{"/index/", http.StatusNotFound},
		{"/index.html", http.StatusOK},
		{"/nonexistent", http.StatusNotFound},
	}

	for _, test := range tests {
		resp, err := http.Get("http://localhost:8000" + test.path)
		if err != nil {
			t.Fatalf("Failed to make GET request: %v", err)
		}

		defer resp.Body.Close()

		if resp.StatusCode != test.statusCode {
			t.Errorf("Expected status code %v, got %v for path %v", test.statusCode, resp.StatusCode, test.path)
		}
	}
}
