package main

import (
	"mime"
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

// TestLabelAsMarkdown pins which responses this server relabels as markdown.
//
// Asserted on the decision rather than on a served Content-Type, because the
// starting point is not the same everywhere: most Linux distributions map .md
// in /etc/mime.types, so http.ServeFile already answers text/markdown there,
// while macOS and the distroless image that ships to production have no entry
// and sniff text/plain. A test that read the header back would pin the host's
// MIME table, not this server's behavior — and would pass or fail by platform.
func TestLabelAsMarkdown(t *testing.T) {
	tests := []struct {
		name          string
		wantsMarkdown bool
		urlPath       string
		filePath      string
		want          bool
	}{
		{"negotiated route", true, "/about", "/site/about.md", true},
		{"nested negotiated route", true, "/docs/intro", "/site/docs/intro.md", true},

		// The case this scoping exists for: the client did not negotiate, so
		// the file keeps whatever type it already had.
		{"direct .md, no Accept", false, "/about.md", "/site/about.md", false},
		{"direct .md, but asking for markdown", true, "/about.md", "/site/about.md", false},

		{"negotiable route that fell through to html", true, "/legal", "/site/legal/index.html", false},
		{"root never negotiates", true, rootPath, "/site/index.html", false},
		{"asset", true, "/style.css", "/site/style.css", false},
		{"client never asked", false, "/about", "/site/about.md", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, labelAsMarkdown(tt.wantsMarkdown, tt.urlPath, tt.filePath))
		})
	}
}

// TestMarkdownContentTypeScope checks the same scoping end to end, in the terms
// a platform can actually agree on.
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

	// What http.ServeFile labels a .md as when this server keeps its hands off:
	// the system MIME table where there is an entry, sniffed text/plain where
	// there is not (Go's built-in table has none, and neither does distroless).
	untouchedType := mime.TypeByExtension(markdownExtension)
	if untouchedType == "" {
		untouchedType = "text/plain; charset=utf-8"
	}

	t.Run("direct .md keeps working and keeps the platform's type", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about.md", http.NoBody)
		rec := httptest.NewRecorder()

		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "markdown source")
		assert.Equal(t, untouchedType, rec.Header().Get("Content-Type"),
			"a direct .md must keep the type it would have had without this server")
		assert.Empty(t, rec.Header().Get("Vary"), "a direct .md never negotiates")
	})

	// A negotiated response is labeled on every platform, including the one
	// that would otherwise sniff it as plain text.
	t.Run("a negotiated response is always labeled markdown", func(t *testing.T) {
		negotiated := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about", http.NoBody)
		negotiated.Header.Set("Accept", "text/markdown")

		negRec := httptest.NewRecorder()

		newHandler().ServeHTTP(negRec, negotiated)

		direct := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about.md", http.NoBody)
		dirRec := httptest.NewRecorder()

		newHandler().ServeHTTP(dirRec, direct)

		assert.Equal(t, negRec.Body.String(), dirRec.Body.String(), "same bytes either way")
		assert.Equal(t, markdownContentType, negRec.Header().Get("Content-Type"))
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

// TestSPAFallbackAdvertisesVary covers the one shape where a single URL yields
// two bodies without either being a negotiated hit: SPA mode, a `.md` on disk,
// and no HTML page beside it. Markdown clients take the hit path and get the
// markdown; browsers fall through to the shell. A cache that stored the shell
// unkeyed would hand it to the next client that asked for markdown.
func TestSPAFallbackAdvertisesVary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<html>shell</html>")
	writeFile(t, dir, "404.html", "<html>404</html>")
	// Deliberately no foo.html and no foo/index.html.
	writeFile(t, dir, "foo.md", "# Foo\n\nmarkdown only\n")

	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))

	newHandler := func() *staticFileHandler {
		return &staticFileHandler{
			fs: fs, staticFilePath: dir, defaultExtension: ".html",
			spaMode: true, next: http.NotFoundHandler(),
		}
	}

	t.Run("the browser reaching the shell still gets Vary", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/foo", http.NoBody)
		req.Header.Set("Accept", "text/html,*/*;q=0.8")

		rec := httptest.NewRecorder()

		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "shell", "control: this really is the SPA fallback")
		assert.Equal(t, "Accept", rec.Header().Get("Vary"))
	})

	// The other half of the pair — proving the two responses really do differ,
	// which is what makes the header above load-bearing.
	t.Run("the agent gets markdown for the same URL", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/foo", http.NoBody)
		req.Header.Set("Accept", "text/markdown")

		rec := httptest.NewRecorder()

		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "markdown only")
		assert.Equal(t, "Accept", rec.Header().Get("Vary"))
	})

	// The root is not negotiable — it is served from index.html for every client
	// alike — so it must not be keyed on Accept even in SPA mode, where it is the
	// most-requested URL the site has.
	t.Run("the root is not keyed on Accept", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, rootPath, http.NoBody)
		req.Header.Set("Accept", "text/markdown")

		rec := httptest.NewRecorder()

		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "shell", "control: the root is served, not missed")
		assert.Empty(t, rec.Header().Get("Vary"))
	})
}

func TestAdvertiseAcceptVaries(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		want     []string
	}{
		{"nothing declared", nil, []string{"Accept"}},

		// A site's own Vary must survive: Set would discard it, and repeated
		// field lines are combined by caches, so this reads as
		// "Accept-Encoding, Accept".
		{"site declared something else", []string{"Accept-Encoding"}, []string{"Accept-Encoding", "Accept"}},

		// ...but naming Accept twice is pointless.
		{"site already declared Accept", []string{"Accept"}, []string{"Accept"}},
		{"site declared Accept in a list", []string{"Accept-Encoding, Accept"}, []string{"Accept-Encoding, Accept"}},
		{"case-insensitive per RFC 9110", []string{"accept"}, []string{"accept"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			for _, v := range tt.existing {
				header.Add("Vary", v)
			}

			advertiseAcceptVaries(header)

			assert.Equal(t, tt.want, header.Values("Vary"))
		})
	}
}

// TestSPAFallbackRootIsNeverKeyed pins the negotiable() guard on the SPA
// fallback. That guard is only observable in one shape — SPA mode with no
// index.html on disk, so the root itself reaches the fallback — because every
// other path that gets there is extensionless and therefore negotiable. Without
// the test the guard would be an unverified assertion rather than a checked one.
func TestSPAFallbackRootIsNeverKeyed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "404.html", "<html>404</html>")

	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))
	h := &staticFileHandler{
		fs: fs, staticFilePath: dir, defaultExtension: ".html",
		spaMode: true, next: http.NotFoundHandler(),
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, rootPath, http.NoBody)
	req.Header.Set("Accept", "text/markdown")

	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// The root is served from index.html for every client alike, so nothing
	// about this response can depend on Accept — whether that file is there or,
	// as here, missing.
	assert.Empty(t, rec.Header().Get("Vary"))
}
