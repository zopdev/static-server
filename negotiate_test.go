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

func TestMarkdownPreferred(t *testing.T) {
	tests := []struct {
		name   string
		accept string
		want   bool
	}{
		{"empty", "", false},
		{"bare markdown", "text/markdown", true},
		{"markdown with charset", "text/markdown; charset=utf-8", true},
		{"legacy spelling", "text/x-markdown", true},
		{"markdown first", "text/markdown, text/html", true},
		{"markdown listed after html, equal q", "text/html, text/markdown", true},

		// The one that matters: a browser's Accept ends in */*;q=0.8, which
		// matches text/markdown by the letter of RFC 9110. Treating that as a
		// request for markdown would serve raw source to every human visitor.
		{"chrome", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8", false},
		{"safari", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", false},
		{"wildcard only", "*/*", false},
		{"curl default", "*/*", false},

		// A client that ranks markdown below HTML expressed a preference.
		{"markdown deprioritised", "text/html, text/markdown;q=0.1", false},
		{"markdown zero q", "text/markdown;q=0", false},
		{"html deprioritised", "text/html;q=0.2, text/markdown;q=0.9", true},
		{"xhtml outranks markdown", "application/xhtml+xml;q=0.9, text/markdown;q=0.5", false},

		{"unrelated types", "application/json, text/plain", false},
		{"malformed entry ignored", "text/markdown, ;;;broken", true},
		{"malformed q ignored", "text/markdown;q=notanumber", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, markdownPreferred(tt.accept))
		})
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()

	path := filepath.Join(dir, name)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("Failed to create dir for %s: %v", name, err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write %s: %v", name, err)
	}
}

// setupNegotiationDir mirrors what a static site generator emits: the rendered
// page as a directory index, with the markdown source as its sibling.
func setupNegotiationDir(t *testing.T) string {
	t.Helper()

	dir := setupTestDir(t)

	writeFile(t, dir, "about/index.html", "<html>about</html>")
	writeFile(t, dir, "about.md", "# About\n\nmarkdown source\n")
	// A page with no markdown sibling — negotiation must fall through to HTML.
	writeFile(t, dir, "legal/index.html", "<html>legal</html>")

	return dir
}

func TestResolveFilePathMarkdownNegotiation(t *testing.T) {
	dir := setupNegotiationDir(t)
	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))

	tests := []struct {
		name     string
		urlPath  string
		accept   string
		wantFile string
	}{
		{"agent gets markdown", "/about", "text/markdown", "about.md"},
		{"browser gets html", "/about", "text/html,*/*;q=0.8", "about/index.html"},
		{"no accept header gets html", "/about", "", "about/index.html"},
		{"falls through when no .md exists", "/legal", "text/markdown", "legal/index.html"},
		{"root is never negotiated", rootPath, "text/markdown", "index.html"},
		{"explicit extension is untouched", "/style.css", "text/markdown", "style.css"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &staticFileHandler{fs: fs, staticFilePath: dir, defaultExtension: ".html"}

			path, _ := h.resolveFilePath(tt.urlPath, tt.accept)

			assert.Equal(t, filepath.Join(dir, tt.wantFile), path)
		})
	}
}

func TestServeHTTPMarkdownNegotiation(t *testing.T) {
	dir := setupNegotiationDir(t)
	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))

	newHandler := func() *staticFileHandler {
		return &staticFileHandler{
			fs:               fs,
			staticFilePath:   dir,
			defaultExtension: ".html",
			next:             http.NotFoundHandler(),
		}
	}

	t.Run("agent receives markdown with the right content type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/about", http.NoBody)
		req.Header.Set("Accept", "text/markdown")

		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "markdown source")
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/markdown")
	})

	t.Run("browser still receives html", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/about", http.NoBody)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "<html>about</html>")
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	})

	t.Run("Vary: Accept is always set so caches do not cross-serve", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/about", http.NoBody)
		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, "Accept", rec.Header().Get("Vary"))
	})

	t.Run("direct .md request keeps working", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/about.md", http.NoBody)
		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "markdown source")
	})
}
