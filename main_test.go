package main

import (
	"net/http"
	"testing"
	"time"
)

func TestServer(t *testing.T) {
	t.Setenv("HTTP_PORT", "1010")

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
		resp, err := http.Get("http://localhost:1010/" + test.path)
		if err != nil {
			t.Fatalf("Failed to make GET request: %v", err)
		}

		defer resp.Body.Close()

		if resp.StatusCode != test.statusCode {
			t.Errorf("Expected status code %v, got %v for path %v", test.statusCode, resp.StatusCode, test.path)
		}
	}
}
