package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gofr.dev/pkg/gofr/config"
)

func TestHydrateConfig(t *testing.T) {
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		expected string
		wantErr  bool
	}{
		{
			name:     "hydrates config in-place",
			template: `{"a":"${A}","b":"${B}"}`,
			vars:     map[string]string{"A": "1", "B": "2"},
			expected: `{"a":"1","b":"2"}`,
			wantErr:  false,
		},
		{
			name:    "no-op when config path empty",
			vars:    map[string]string{},
			wantErr: false,
		},
		{
			name:     "extra config vars not in template",
			template: `{"a":"${A}"}`,
			vars:     map[string]string{"A": "1", "EXTRA": "x"},
			expected: `{"a":"1"}`,
			wantErr:  false,
		},
		{
			name:     "some template vars missing",
			template: `{"a":"${A}","b":"${MISSING}"}`,
			vars:     map[string]string{"A": "1"},
			expected: `{"a":"1","b":""}`,
			wantErr:  true,
		},
		{
			name:     "all template vars missing",
			template: `{"a":"${X}","b":"${Y}"}`,
			vars:     map[string]string{},
			expected: `{"a":"","b":""}`,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := make(map[string]string)
			for k, v := range tt.vars {
				vars[k] = v
			}

			if tt.template != "" {
				dir := t.TempDir()
				configFile := filepath.Join(dir, "config.json")
				if err := os.WriteFile(configFile, []byte(tt.template), 0644); err != nil {
					t.Fatalf("failed to write config: %v", err)
				}
				vars["CONFIG_FILE_PATH"] = configFile
			}

			cfg := config.NewMockConfig(vars)
			err := hydrateConfig(cfg)

			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.template != "" {
				output, err := os.ReadFile(vars["CONFIG_FILE_PATH"])
				if err != nil {
					t.Fatalf("failed to read config: %v", err)
				}
				if string(output) != tt.expected {
					t.Errorf("expected %q, got %q", tt.expected, string(output))
				}
			}
		})
	}
}

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
