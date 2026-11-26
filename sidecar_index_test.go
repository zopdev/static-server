package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gofr.dev/pkg/gofr/datasource/file"
	"gofr.dev/pkg/gofr/logging"
)

// countingFS records every Stat, ReadDir and Open the handler makes, so a test
// can prove what a request costs on disk rather than only what it returns.
type countingFS struct {
	file.FileSystem

	mu    sync.Mutex
	calls []string
}

func (c *countingFS) record(op, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls = append(c.calls, op+" "+name)
}

func (c *countingFS) Stat(name string) (file.FileInfo, error) {
	c.record("stat", name)

	return c.FileSystem.Stat(name)
}

func (c *countingFS) ReadDir(dir string) ([]file.FileInfo, error) {
	c.record("readdir", dir)

	return c.FileSystem.ReadDir(dir)
}

func (c *countingFS) reset() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	calls := c.calls
	c.calls = nil

	return calls
}

func newCountingHandler(t *testing.T, dir string) (*staticFileHandler, *countingFS) {
	t.Helper()

	fs := &countingFS{FileSystem: file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))}
	h := &staticFileHandler{
		fs:               fs,
		staticFilePath:   dir,
		defaultExtension: ".html",
		next:             http.NotFoundHandler(),
		headerRules:      loadHeaderRules(fs, dir),
	}

	index, err := buildSidecarIndex(fs, dir)
	require.NoError(t, err)

	h.sidecars = index

	fs.reset() // startup cost is not request cost

	return h, fs
}

// TestRequestsNeverProbeForSidecars is the review's F1: a file request must not
// stat `.br`/`.gz` candidates. With no sidecars on disk the request makes the
// same filesystem calls it made before precompression existed (one Stat of the
// resolved file); with sidecars it makes no extra calls either.
func TestRequestsNeverProbeForSidecars(t *testing.T) {
	for name, withSidecars := range map[string]bool{"site without sidecars": false, "site with sidecars": true} {
		t.Run(name, func(t *testing.T) {
			dir := setupTestDir(t)
			writeFile(t, dir, "app.js", strings.Repeat("x", 2048))

			if withSidecars {
				writeFile(t, dir, "app.js.br", "br-bytes")
				writeFile(t, dir, "app.js.gz", "gz-bytes")
			}

			h, fs := newCountingHandler(t, dir)

			for _, ae := range []string{"", "br", "gzip", "gzip, deflate, br, zstd"} {
				serve(t, h, http.MethodGet, "/app.js", map[string]string{acceptEncodingHeader: ae})

				calls := fs.reset()
				assert.Equal(t, []string{"stat " + filepath.Join(dir, "app.js")}, calls, "Accept-Encoding %q", ae)
			}
		})
	}
}

// TestSidecarIndexKeysMatchResolvedPaths guards the silent failure mode: an
// index keyed differently from resolveFilePath would serve nothing compressed
// while every response still looked correct.
func TestSidecarIndexKeysMatchResolvedPaths(t *testing.T) {
	dir := setupTestDir(t)

	files := map[string]string{
		"blog/index.html.br":       "br",
		"docs/deep/page.html":      "<html>deep</html>",
		"docs/deep/page.html.br":   "br",
		"about.md":                 "# about",
		"about.md.br":              "br",
		"résumé/café.css":          "a{}",
		"résumé/café.css.br":       "br",
		"orphan.js.br":             "br",
		".well-known/mcp.json":     "{}",
		".well-known/mcp.json.br":  "br",
		"nested/.well-known/x.txt": "x",
	}
	for name, content := range files {
		writeFile(t, dir, name, content)
	}

	writeFile(t, dir, "nested/.well-known/x.txt.br", "br")

	index := mustIndex(file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR)), dir)

	h := newEncodingHandler(dir)
	for _, urlPath := range []string{"/blog", "/docs/deep/page", "/résumé/café.css"} {
		resolved, _ := h.resolveFilePath(urlPath, false)
		assert.Contains(t, index, resolved, urlPath)
	}

	resolvedMD, _ := h.resolveFilePath("/about", true)
	assert.Contains(t, index, resolvedMD, "negotiated markdown")

	assert.NotContains(t, index, filepath.Join(dir, "orphan.js"), "orphan sidecar")
	assert.NotContains(t, index, filepath.Join(dir, ".well-known", "mcp.json"))
	assert.NotContains(t, index, filepath.Join(dir, "nested", ".well-known", "x.txt"))
	assert.Equal(t, []precompressed{{coding: "br", suffix: ".br"}}, index[filepath.Join(dir, "blog", "index.html")])
}

func TestSidecarIndexDoesNotFollowSymlinkedDirs(t *testing.T) {
	dir := setupTestDir(t)
	outside := t.TempDir()

	writeFile(t, outside, "leak.css", "a{}")
	writeFile(t, outside, "leak.css.br", "br")
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "linked")))

	index := mustIndex(file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR)), dir)

	assert.NotContains(t, index, filepath.Join(dir, "linked", "leak.css"))
}

// TestSidecarIndexKeepsGoingPastUnreadableDirs: startup must never fail on the
// index (gofr exits when an OnStart hook errors), and one bad directory must not
// cost the rest of the site its compression.
func TestSidecarIndexKeepsGoingPastUnreadableDirs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 000 directory")
	}

	dir := setupTestDir(t)
	writeFile(t, dir, "style.css.br", "br")
	writeFile(t, dir, "locked/a.css", "a{}")
	writeFile(t, dir, "locked/a.css.br", "br")
	require.NoError(t, os.Chmod(filepath.Join(dir, "locked"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked"), 0o700) })

	index, err := buildSidecarIndex(file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR)), dir)

	require.Error(t, err)
	assert.Contains(t, index, filepath.Join(dir, "style.css"))
	assert.NotContains(t, index, filepath.Join(dir, "locked", "a.css"))
}

func TestSidecarIndexMissingRootIsEmpty(t *testing.T) {
	index, err := buildSidecarIndex(file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR)),
		filepath.Join(t.TempDir(), "missing"))

	require.Error(t, err)
	assert.Equal(t, 0, index.size())
}

// BenchmarkServeFile measures one GET of a 2 KB file, with and without a
// sidecar, for an identity and a br client: the hot path the review is about.
func BenchmarkServeFile(b *testing.B) {
	for _, withSidecar := range []bool{false, true} {
		dir := b.TempDir()
		require.NoError(b, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o600))
		require.NoError(b, os.WriteFile(filepath.Join(dir, "app.js"), []byte(strings.Repeat("x", 2048)), 0o600))

		if withSidecar {
			require.NoError(b, os.WriteFile(filepath.Join(dir, "app.js.br"), []byte("br-bytes"), 0o600))
		}

		h := newEncodingHandler(dir)

		for _, ae := range []string{"", "br"} {
			name := map[bool]string{false: "no-sidecar", true: "sidecar"}[withSidecar] + "/ae=" + map[string]string{"": "identity", "br": "br"}[ae]
			b.Run(name, func(b *testing.B) {
				req, _ := http.NewRequestWithContext(b.Context(), http.MethodGet, "/app.js", http.NoBody)
				if ae != "" {
					req.Header.Set(acceptEncodingHeader, ae)
				}

				b.ReportAllocs()

				for b.Loop() {
					h.ServeHTTP(discardWriter{header: http.Header{}}, req)
				}
			})
		}
	}
}

type discardWriter struct{ header http.Header }

func (d discardWriter) Header() http.Header       { clear(d.header); return d.header }
func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriter) WriteHeader(int)             {}
