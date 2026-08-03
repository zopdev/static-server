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

			path, _ := h.resolveFilePath(tt.urlPath, markdownPreferred(tt.accept))

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
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about", http.NoBody)
		req.Header.Set("Accept", "text/markdown")

		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "markdown source")
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/markdown")
	})

	t.Run("browser still receives html", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about", http.NoBody)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "<html>about</html>")
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	})

	t.Run("Vary: Accept is set on a route that can negotiate", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about", http.NoBody)
		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, "Accept", rec.Header().Get("Vary"))
	})

	t.Run("a miss is answered in markdown, not a large HTML shell", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/no/such/page", http.NoBody)
		req.Header.Set("Accept", "text/markdown")

		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/markdown")
		assert.Contains(t, rec.Body.String(), "404 Not Found")
		// Recovery pointers, so the reader can find real URLs itself.
		assert.Contains(t, rec.Body.String(), "/sitemap.xml")
		// An HTML 404 shell on a real site measured 144 KB.
		assert.Less(t, rec.Body.Len(), 1024)
	})

	t.Run("a browser still gets the HTML 404 page", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/no/such/page", http.NoBody)
		req.Header.Set("Accept", "text/html,*/*;q=0.8")

		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "404")
		assert.NotContains(t, rec.Header().Get("Content-Type"), "markdown")
	})
}

// TestMarkdownContentTypeScope pins which responses get relabelled as markdown.
//
// The scope is deliberately narrow. A directly requested .md is not a
// negotiated response and must keep the type it resolves to today: browsers
// render text/plain inline but download text/markdown, so relabelling it would
// turn every existing .md link on a site into a download prompt.
func TestMarkdownContentTypeScope(t *testing.T) {
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

	t.Run("direct .md keeps working and keeps its type", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about.md", http.NoBody)
		rec := httptest.NewRecorder()

		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "markdown source")
		assert.NotContains(t, rec.Header().Get("Content-Type"), "text/markdown",
			"a direct .md must keep the type it has today")
		assert.Empty(t, rec.Header().Get("Vary"), "a direct .md never negotiates")
	})

	// The same bytes reached two ways: only the negotiated route relabels them.
	t.Run("the same file is text/markdown only when negotiated", func(t *testing.T) {
		negotiated := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about", http.NoBody)
		negotiated.Header.Set("Accept", "text/markdown")

		negRec := httptest.NewRecorder()

		newHandler().ServeHTTP(negRec, negotiated)

		direct := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about.md", http.NoBody)
		dirRec := httptest.NewRecorder()

		newHandler().ServeHTTP(dirRec, direct)

		assert.Equal(t, negRec.Body.String(), dirRec.Body.String(), "same bytes either way")
		assert.Contains(t, negRec.Header().Get("Content-Type"), "text/markdown")
		assert.NotContains(t, dirRec.Header().Get("Content-Type"), "text/markdown")
	})

	// The root is served straight from index.html and never negotiates, so it
	// must not advertise Vary either — it is usually the most-cached URL a site
	// has, and fragmenting it on Accept buys nothing.
	t.Run("the root never negotiates and never varies", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, rootPath, http.NoBody)
		req.Header.Set("Accept", "text/markdown")

		rec := httptest.NewRecorder()

		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "<html>index</html>", "root is always index.html")
		assert.Empty(t, rec.Header().Get("Vary"), "the root cannot vary by Accept")
		assert.NotContains(t, rec.Header().Get("Content-Type"), "text/markdown")
	})

	// A miss answers in markdown whenever asked, whatever the path shape — so
	// even a path that never negotiates on a hit must still advertise Vary, or
	// a cache can hand an agent the HTML shell it stored for a browser.
	t.Run("a miss advertises Vary even on an extensioned path", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/gone.html", http.NoBody)
		req.Header.Set("Accept", "text/markdown")

		rec := httptest.NewRecorder()

		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "Accept", rec.Header().Get("Vary"))
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/markdown")
	})
}
