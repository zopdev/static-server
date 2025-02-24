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

		resp.Body.Close()
	}

	//nolint:staticcheck // Ignore as we are testing the server
	os.RemoveAll(tempDir)
}

func TestServer_FileAndDirectorySameName(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Create an "index.html" file
	indexFilePath := filepath.Join(tempDir, "index.html")
	if err := os.WriteFile(indexFilePath, []byte("<html><body>Index File</body></html>"), 0600); err != nil {
		t.Fatalf("Failed to write file %s: %v", indexFilePath, err)
	}

	// Create an "index" directory
	indexDirPath := filepath.Join(tempDir, "index")
	if err := os.MkdirAll(indexDirPath, 0600); err != nil {
		t.Fatalf("Failed to create directory %s: %v", indexDirPath, err)
	}

	// Create an "index/index.html" file inside the directory
	indexDirFilePath := filepath.Join(indexDirPath, "index.html")
	if err := os.WriteFile(indexDirFilePath, []byte("<html><body>Index Directory</body></html>"), 0600); err != nil {
		t.Fatalf("Failed to write file %s: %v", indexDirFilePath, err)
	}

	// Set the environment variable for the static file path
	t.Setenv("STATIC_DIR_PATH", tempDir)

	go main()

	time.Sleep(3 * time.Second) // Allow time for the server to start

	tests := []struct {
		path       string
		statusCode int
	}{
		{"/index", http.StatusOK},  // Should serve "index.html" file
		{"/index/", http.StatusOK}, // Should serve "index/index.html"
		{"/index.html", http.StatusOK},
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

		resp.Body.Close()
	}
}
