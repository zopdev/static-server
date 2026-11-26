package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gofr.dev/pkg/gofr/datasource/file"
	"gofr.dev/pkg/gofr/logging"
)

func TestParseAcceptEncoding(t *testing.T) {
	tests := []struct {
		name     string
		values   []string
		wantBr   bool
		wantGzip bool
	}{
		{"absent", nil, false, false},
		{"empty", []string{""}, false, false},
		{"br", []string{"br"}, true, false},
		{"gzip", []string{"gzip"}, false, true},
		{"chrome", []string{"gzip, deflate, br, zstd"}, true, true},
		{"identity only", []string{"identity"}, false, false},
		{"identity refused", []string{"identity;q=0"}, false, false},
		{"wildcard", []string{"*"}, true, true},
		{"wildcard refused", []string{"*;q=0"}, false, false},
		{"named coding beats wildcard", []string{"*, br;q=0"}, false, true},
		{"wildcard does not override a named refusal", []string{"br;q=0, *"}, false, true},
		{"q=0 refuses", []string{"br;q=0, gzip"}, false, true},
		{"q=0.0 refuses", []string{"br;q=0.0"}, false, false},
		{"q=0.000 refuses", []string{"br;q=0.000"}, false, false},
		{"low but positive q accepts", []string{"br;q=0.001"}, true, false},
		{"q=1.000 accepts", []string{"br;q=1.000"}, true, false},
		{"uppercase coding", []string{"BR, GZip"}, true, true},
		{"uppercase q parameter", []string{"br;Q=0"}, false, false},
		{"whitespace everywhere", []string{"  br ;  q=0.5 ,\tgzip  "}, true, true},
		{"empty elements skipped", []string{", ,br,"}, true, false},
		{"other parameters ignored", []string{"br;foo=bar;q=1"}, true, false},

		// Malformed weights are not consent — not even when `*` would allow it.
		{"q not a number", []string{"br;q=abc"}, false, false},
		{"q above one", []string{"br;q=2"}, false, false},
		{"q too precise", []string{"br;q=0.0001"}, false, false},
		{"q NaN", []string{"br;q=NaN"}, false, false},
		{"q empty", []string{"br;q="}, false, false},
		{"q negative", []string{"br;q=-1"}, false, false},
		{"malformed br with wildcard", []string{"br;q=abc, *"}, false, true},
		{"malformed wildcard", []string{"*;q=abc"}, false, false},

		// Repeated field lines are one list.
		{"two header lines", []string{"gzip", "br"}, true, true},
		{"refusal on a second line wins", []string{"br", "br;q=0"}, false, false},
		{"refusal on a first line wins", []string{"br;q=0", "br"}, false, false},

		{"very long header", []string{strings.Repeat("x-unknown, ", 2000) + "br"}, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseAcceptEncoding(tt.values)

			assert.Equal(t, tt.wantBr, parsed.accepts("br"), "br")
			assert.Equal(t, tt.wantGzip, parsed.accepts("gzip"), "gzip")
		})
	}
}

const (
	encodedCSS = "body { color: red; } /* the original stylesheet, long enough to matter */"
	encodedMD  = "# About\n\nmarkdown source\n"
)

// fakeBrotli stands in for a brotli sidecar. The server never decodes what it
// serves, so a recognizable payload proves the bytes on the wire are the
// sidecar's; the gzip sidecars below are real and are decoded.
func fakeBrotli(original string) string {
	return "\x8b\x00BROTLI(" + original + ")"
}

func gzipped(t *testing.T, original string) string {
	t.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)

	_, err := zw.Write([]byte(original))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	return buf.String()
}

func gunzip(t *testing.T, body []byte) string {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)

	out, err := io.ReadAll(zr)
	require.NoError(t, err)

	return string(out)
}

// setupEncodingDir is a site whose build emitted sidecars for some files and
// not others.
func setupEncodingDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	files := map[string]string{
		"index.html":    "<html>index</html>",
		"index.html.br": fakeBrotli("<html>index</html>"),
		"404.html":      "<html>404</html>",

		"style.css":    encodedCSS,
		"style.css.br": fakeBrotli(encodedCSS),
		"style.css.gz": gzipped(t, encodedCSS),

		// gzip only, no br.
		"legacy.js":    "console.log('legacy');",
		"legacy.js.gz": gzipped(t, "console.log('legacy');"),

		// No sidecar at all.
		"plain.js": "console.log('plain');",

		"blog/index.html":    "<html>blog</html>",
		"blog/index.html.br": fakeBrotli("<html>blog</html>"),

		"about/index.html":    "<html>about</html>",
		"about/index.html.br": fakeBrotli("<html>about</html>"),
		"about.md":            encodedMD,
		"about.md.br":         fakeBrotli(encodedMD),

		// An extension neither Go nor any mime.types knows: the type must be
		// sniffed from the original's text, never from the sidecar's bytes.
		"notes.zzunknown":    "plain text notes",
		"notes.zzunknown.br": "\x00\x01\x02\xff binary",

		// Orphans: a sidecar whose original is gone.
		"ghost.css.br":  fakeBrotli("ghost"),
		"ghost.html.br": fakeBrotli("ghost"),
	}

	for name, content := range files {
		writeFile(t, dir, name, content)
	}

	// Distinct, fixed times for the original and its sidecars, so a
	// Last-Modified taken from the wrong file — or from the clock — shows.
	for name, when := range map[string]time.Time{
		"style.css":    originalModTime(),
		"style.css.br": originalModTime().Add(time.Hour),
		"style.css.gz": originalModTime().Add(2 * time.Hour),
	} {
		require.NoError(t, os.Chtimes(filepath.Join(dir, name), when, when))
	}

	return dir
}

// originalModTime is the fixed modification time of the fixture's style.css.
func originalModTime() time.Time {
	return time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
}

func newEncodingHandler(dir string) *staticFileHandler {
	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))

	return &staticFileHandler{
		fs:               fs,
		staticFilePath:   dir,
		defaultExtension: ".html",
		next:             http.NotFoundHandler(),
		headerRules:      loadHeaderRules(fs, dir),
		sidecars:         mustIndex(fs, dir),
	}
}

func mustIndex(fs file.FileSystem, dir string) sidecarIndex {
	index, err := buildSidecarIndex(fs, dir)
	if err != nil {
		panic(err)
	}

	return index
}

func serve(t *testing.T, h http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)

	for name, value := range header {
		req.Header.Set(name, value)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func varies(rec *httptest.ResponseRecorder, field string) bool {
	for _, line := range rec.Header().Values("Vary") {
		for _, named := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(named), field) {
				return true
			}
		}
	}

	return false
}

func TestServePrecompressedSelection(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	tests := []struct {
		name           string
		urlPath        string
		acceptEncoding string
		wantEncoding   string
		wantBody       string
	}{
		{"br", "/style.css", "br", "br", fakeBrotli(encodedCSS)},
		{"chrome gets br", "/style.css", "gzip, deflate, br, zstd", "br", fakeBrotli(encodedCSS)},
		{"br preferred even at a lower q", "/style.css", "gzip;q=1, br;q=0.1", "br", fakeBrotli(encodedCSS)},
		{"gzip only", "/style.css", "gzip", "gzip", gzipped(t, encodedCSS)},
		{"br refused falls to gzip", "/style.css", "br;q=0, gzip", "gzip", gzipped(t, encodedCSS)},
		{"wildcard", "/style.css", "*", "br", fakeBrotli(encodedCSS)},
		{"wildcard refused", "/style.css", "*;q=0", "", encodedCSS},
		{"identity", "/style.css", "identity", "", encodedCSS},
		{"absent", "/style.css", "", "", encodedCSS},
		{"uppercase", "/style.css", "BR", "br", fakeBrotli(encodedCSS)},
		{"malformed q", "/style.css", "br;q=abc", "", encodedCSS},
		{"q above one", "/style.css", "br;q=2", "", encodedCSS},
		{"br wanted but only gzip on disk", "/legacy.js", "br", "", "console.log('legacy');"},
		{"gzip sidecar used when br has none", "/legacy.js", "br, gzip", "gzip", gzipped(t, "console.log('legacy');")},
		{"root", rootPath, "br", "br", fakeBrotli("<html>index</html>")},
		{"directory index", "/blog", "br", "br", fakeBrotli("<html>blog</html>")},
		{"directory index with trailing slash", "/blog/", "br", "br", fakeBrotli("<html>blog</html>")},
		{"negotiable route, browser", "/about", "br", "br", fakeBrotli("<html>about</html>")},
		{"direct .md", "/about.md", "br", "br", fakeBrotli(encodedMD)},
		{"no sidecar", "/plain.js", "br, gzip", "", "console.log('plain');"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := map[string]string{}

			if tt.acceptEncoding != "" {
				header[acceptEncodingHeader] = tt.acceptEncoding
			}

			rec := serve(t, h, http.MethodGet, tt.urlPath, header)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.wantEncoding, rec.Header().Get(contentEncodingHeader))
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}

// TestEncodedResponseHeaders pins every header an encoded response differs in,
// and those it must not differ in.
func TestEncodedResponseHeaders(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	plain := serve(t, h, http.MethodGet, "/style.css", nil)
	encoded := serve(t, h, http.MethodGet, "/style.css", map[string]string{acceptEncodingHeader: "br"})

	assert.Equal(t, http.StatusOK, encoded.Code)
	assert.Equal(t, "br", encoded.Header().Get(contentEncodingHeader))
	assert.Equal(t, "text/css; charset=utf-8", encoded.Header().Get(contentTypeHeader))
	assert.Equal(t, plain.Header().Get(contentTypeHeader), encoded.Header().Get(contentTypeHeader))
	assert.Equal(t, strconv.Itoa(len(fakeBrotli(encodedCSS))), encoded.Header().Get("Content-Length"))
	assert.Empty(t, encoded.Header().Get("Accept-Ranges"), "ranges are not offered on an encoded body")
	assert.NotEmpty(t, encoded.Header().Get("Last-Modified"))
	assert.Equal(t, originalModTime().Format(http.TimeFormat), encoded.Header().Get("Last-Modified"),
		"Last-Modified comes from the original, not the sidecar")
	assert.Equal(t, plain.Header().Get("Last-Modified"), encoded.Header().Get("Last-Modified"))
	assert.Equal(t, []string{"Accept-Encoding"}, encoded.Header().Values("Vary"))

	// The plain response for a file that has a sidecar must say it varies too.
	assert.Empty(t, plain.Header().Get(contentEncodingHeader))
	assert.Equal(t, []string{"Accept-Encoding"}, plain.Header().Values("Vary"))
	assert.Equal(t, "bytes", plain.Header().Get("Accept-Ranges"), "plain keeps v1.0.0 range support")
}

func TestGzipSidecarDecodesToOriginal(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	rec := serve(t, h, http.MethodGet, "/style.css", map[string]string{acceptEncodingHeader: "gzip"})

	assert.Equal(t, "gzip", rec.Header().Get(contentEncodingHeader))
	assert.Equal(t, encodedCSS, gunzip(t, rec.Body.Bytes()))
}

// TestNoSidecarIsUntouched is the unit-level half of the G2 gate: with no
// sidecar on disk the hook must not write a single header, and every request
// shape must produce the same response whatever Accept-Encoding says.
func TestNoSidecarIsUntouched(t *testing.T) {
	dir := setupTestDir(t)
	writeFile(t, dir, "_headers", realHeadersFile)
	writeFile(t, dir, "about/index.html", "<html>about</html>")
	writeFile(t, dir, "about.md", encodedMD)

	h := newEncodingHandler(dir)

	paths := []string{rootPath, "/docs", "/blog", "/blog/", "/blog/index.html", "/style.css",
		"/about", "/about.md", "/data.json", "/missing", "/missing.css"}
	variants := map[string]map[string]string{
		"plain":       nil,
		"markdown":    {"Accept": "text/markdown"},
		"range":       {"Range": "bytes=0-3"},
		"conditional": {"If-Modified-Since": "Mon, 01 Jan 2035 00:00:00 GMT"},
	}
	encodings := []string{"br", "gzip, deflate, br, zstd", "*", "identity"}

	type request struct {
		method, path string
		header       map[string]string
	}

	methods := []string{http.MethodGet, http.MethodHead}
	cases := make([]request, 0, len(methods)*len(paths)*len(variants))

	for _, method := range methods {
		for _, p := range paths {
			for _, header := range variants {
				cases = append(cases, request{method: method, path: p, header: header})
			}
		}
	}

	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			baseline := serve(t, h, c.method, c.path, c.header)

			for _, ae := range encodings {
				withAE := maps.Clone(c.header)
				if withAE == nil {
					withAE = map[string]string{}
				}

				withAE[acceptEncodingHeader] = ae

				rec := serve(t, h, c.method, c.path, withAE)

				assert.Equal(t, baseline.Code, rec.Code, ae)
				assert.Equal(t, baseline.Header(), rec.Header(), ae)
				assert.Equal(t, baseline.Body.String(), rec.Body.String(), ae)
			}
		})
	}

	t.Run("the hook writes nothing at all", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/style.css", http.NoBody)
		req.Header.Set(acceptEncodingHeader, "br")

		rec := httptest.NewRecorder()
		info, err := h.fs.Stat(h.staticFilePath + "/style.css")
		require.NoError(t, err)

		assert.False(t, h.servePrecompressed(rec, req, h.staticFilePath+"/style.css", info.ModTime()))
		assert.Empty(t, rec.Header())
		assert.Empty(t, rec.Body.String())
		assert.False(t, rec.Flushed)
	})
}

func TestIndexHTMLRedirectIsNeverEncoded(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	for _, target := range []string{"/index.html", "/blog/index.html"} {
		t.Run(target, func(t *testing.T) {
			rec := serve(t, h, http.MethodGet, target, map[string]string{acceptEncodingHeader: "br"})

			assert.Equal(t, http.StatusMovedPermanently, rec.Code)
			assert.Empty(t, rec.Header().Get(contentEncodingHeader))
			assert.False(t, varies(rec, acceptEncodingHeader))
		})
	}
}

func TestOrphanSidecarNeverAnswers(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	for _, target := range []string{"/ghost.css", "/ghost"} {
		t.Run(target, func(t *testing.T) {
			rec := serve(t, h, http.MethodGet, target, map[string]string{acceptEncodingHeader: "br"})

			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.Empty(t, rec.Header().Get(contentEncodingHeader))
			assert.NotContains(t, rec.Body.String(), "ghost")
		})
	}
}

// A direct request for a sidecar is just a file with an unknown extension, as
// in v1.0.0: no Content-Encoding, the stored bytes, and no `.br.br` lookup.
func TestDirectSidecarRequestIsUnchanged(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	rec := serve(t, h, http.MethodGet, "/style.css.br", map[string]string{acceptEncodingHeader: "br"})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(contentEncodingHeader))
	assert.False(t, varies(rec, acceptEncodingHeader))
	assert.Equal(t, fakeBrotli(encodedCSS), rec.Body.String())
}

const markdownHeadersFile = `/*
  X-Frame-Options: DENY
  Vary: Accept-Encoding

/_astro/*
  Cache-Control: public, max-age=31536000, immutable

/*.md
  Content-Type: text/markdown; charset=utf-8
  X-Robots-Tag: noindex
`

func TestEncodedResponseKeepsHeaderRules(t *testing.T) {
	dir := setupEncodingDir(t)
	writeFile(t, dir, "_headers", markdownHeadersFile)
	writeFile(t, dir, "_astro/app.js", "console.log('app');")
	writeFile(t, dir, "_astro/app.js.br", fakeBrotli("console.log('app');"))

	h := newEncodingHandler(dir)

	t.Run("hashed asset keeps its cache and security headers", func(t *testing.T) {
		rec := serve(t, h, http.MethodGet, "/_astro/app.js", map[string]string{acceptEncodingHeader: "br"})

		assert.Equal(t, "br", rec.Header().Get(contentEncodingHeader))
		assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
		assert.Equal(t, []string{"Accept-Encoding"}, rec.Header().Values("Vary"),
			"a Vary the site declared is not repeated")
	})

	t.Run("direct .md keeps the pinned markdown type", func(t *testing.T) {
		rec := serve(t, h, http.MethodGet, "/about.md", map[string]string{acceptEncodingHeader: "br"})

		assert.Equal(t, "br", rec.Header().Get(contentEncodingHeader))
		assert.Equal(t, "text/markdown; charset=utf-8", rec.Header().Get(contentTypeHeader))
		assert.Equal(t, "noindex", rec.Header().Get("X-Robots-Tag"))
		assert.Equal(t, fakeBrotli(encodedMD), rec.Body.String())
	})
}

func TestEncodedContentTypeComesFromTheOriginal(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	// No `_headers`. Whatever this platform's MIME table says about .md, the
	// encoded response must say the same as the plain one — and on a platform
	// with no entry (distroless) that means sniffing the original's text.
	t.Run("direct .md matches the plain response", func(t *testing.T) {
		plain := serve(t, h, http.MethodGet, "/about.md", nil)
		encoded := serve(t, h, http.MethodGet, "/about.md", map[string]string{acceptEncodingHeader: "br"})

		assert.Equal(t, "br", encoded.Header().Get(contentEncodingHeader))
		assert.Equal(t, plain.Header().Get(contentTypeHeader), encoded.Header().Get(contentTypeHeader))
		assert.NotContains(t, encoded.Header().Get(contentTypeHeader), "octet-stream")
	})

	// An extension no MIME table knows forces the sniff, so this pins it on
	// every platform: the sidecar's binary bytes would sniff octet-stream.
	t.Run("unknown extension is sniffed from the original", func(t *testing.T) {
		encoded := serve(t, h, http.MethodGet, "/notes.zzunknown", map[string]string{acceptEncodingHeader: "br"})

		assert.Equal(t, "br", encoded.Header().Get(contentEncodingHeader))
		assert.Equal(t, "text/plain; charset=utf-8", encoded.Header().Get(contentTypeHeader))
	})

	t.Run("negotiated markdown keeps text/markdown", func(t *testing.T) {
		rec := serve(t, h, http.MethodGet, "/about", map[string]string{
			"Accept":             "text/markdown",
			acceptEncodingHeader: "br",
		})

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "br", rec.Header().Get(contentEncodingHeader))
		assert.Equal(t, markdownContentType, rec.Header().Get(contentTypeHeader))
		assert.Equal(t, fakeBrotli(encodedMD), rec.Body.String(), "the .md's own sidecar")
		assert.True(t, varies(rec, "Accept"))
		assert.True(t, varies(rec, acceptEncodingHeader))
	})

	t.Run("negotiated markdown without br", func(t *testing.T) {
		rec := serve(t, h, http.MethodGet, "/about", map[string]string{"Accept": "text/markdown"})

		assert.Empty(t, rec.Header().Get(contentEncodingHeader))
		assert.Equal(t, markdownContentType, rec.Header().Get(contentTypeHeader))
		assert.Equal(t, encodedMD, rec.Body.String())
		assert.True(t, varies(rec, acceptEncodingHeader))
	})
}

func TestEncodedHEAD(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	get := serve(t, h, http.MethodGet, "/style.css", map[string]string{acceptEncodingHeader: "br"})
	head := serve(t, h, http.MethodHead, "/style.css", map[string]string{acceptEncodingHeader: "br"})

	assert.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.String())
	assert.Equal(t, strconv.Itoa(len(fakeBrotli(encodedCSS))), head.Header().Get("Content-Length"),
		"HEAD reports the encoded length")
	assert.Equal(t, get.Header(), head.Header(), "HEAD headers match GET")
}

func TestEncodedRange(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	t.Run("with br the range is ignored and the whole body sent", func(t *testing.T) {
		rec := serve(t, h, http.MethodGet, "/style.css", map[string]string{
			acceptEncodingHeader: "br",
			"Range":              "bytes=0-3",
		})

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "br", rec.Header().Get(contentEncodingHeader))
		assert.Empty(t, rec.Header().Get("Content-Range"))
		assert.Empty(t, rec.Header().Get("Accept-Ranges"))
		assert.Equal(t, fakeBrotli(encodedCSS), rec.Body.String())
	})

	t.Run("an unsatisfiable range is also ignored with br", func(t *testing.T) {
		rec := serve(t, h, http.MethodGet, "/style.css", map[string]string{
			acceptEncodingHeader: "br",
			"Range":              "bytes=99999-",
		})

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, fakeBrotli(encodedCSS), rec.Body.String())
	})

	t.Run("without br the range is honored as before", func(t *testing.T) {
		rec := serve(t, h, http.MethodGet, "/style.css", map[string]string{"Range": "bytes=0-3"})

		assert.Equal(t, http.StatusPartialContent, rec.Code)
		assert.Empty(t, rec.Header().Get(contentEncodingHeader))
		assert.Equal(t, "bytes 0-3/"+strconv.Itoa(len(encodedCSS)), rec.Header().Get("Content-Range"))
		assert.Equal(t, encodedCSS[:4], rec.Body.String())
		assert.True(t, varies(rec, acceptEncodingHeader))
	})
}

func TestEncodedConditionalRequests(t *testing.T) {
	dir := setupEncodingDir(t)
	h := newEncodingHandler(dir)

	lastModified := serve(t, h, http.MethodGet, "/style.css", nil).Header().Get("Last-Modified")
	require.NotEmpty(t, lastModified)

	tests := []struct {
		name       string
		since      string
		wantStatus int
	}{
		{"unchanged since", lastModified, http.StatusNotModified},
		{"changed since", "Mon, 01 Jan 2001 00:00:00 GMT", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, ae := range []string{"", "br", "gzip"} {
				header := map[string]string{"If-Modified-Since": tt.since}

				if ae != "" {
					header[acceptEncodingHeader] = ae
				}

				rec := serve(t, h, http.MethodGet, "/style.css", header)

				assert.Equal(t, tt.wantStatus, rec.Code, "same decision for encoding %q", ae)
				assert.True(t, varies(rec, acceptEncodingHeader), "encoding %q", ae)

				if tt.wantStatus == http.StatusNotModified {
					assert.Empty(t, rec.Header().Get(contentEncodingHeader))
					assert.Empty(t, rec.Body.String())
				}
			}
		})
	}
}

func TestEncodedResponseWriterKeepsReaderFrom(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &encodedResponseWriter{ResponseWriter: rec, size: 3}

	w.WriteHeader(http.StatusOK)

	n, err := w.ReadFrom(strings.NewReader("abc"))

	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.Equal(t, "abc", rec.Body.String())
	assert.Equal(t, rec, w.Unwrap())
}

// TestOpenInRootRefusesEscapes pins the containment guard behind every sidecar
// and sniff open: a path outside the static root, or a symlink inside it that
// points out, is refused by the OS rather than read.
func TestOpenInRootRefusesEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	writeFile(t, root, "ok.css.br", "inside")
	writeFile(t, outside, "secret.css.br", "outside")
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.css.br"), filepath.Join(root, "link.css.br")))

	f, err := openInRoot(root, filepath.Join(root, "ok.css.br"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	for name, p := range map[string]string{
		"sibling dir":      filepath.Join(outside, "secret.css.br"),
		"dot-dot":          filepath.Join(root, "..", filepath.Base(outside), "secret.css.br"),
		"escaping symlink": filepath.Join(root, "link.css.br"),
	} {
		t.Run(name, func(t *testing.T) {
			f, err := openInRoot(root, p)
			if f != nil {
				_ = f.Close()
			}

			require.Error(t, err)
		})
	}
}
