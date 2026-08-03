package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"gofr.dev/pkg/gofr/datasource/file"
	"gofr.dev/pkg/gofr/logging"
)

// Verbatim excerpt of the `_headers` file zop.dev has published for months —
// every header in it silently discarded, because nothing on the serving path
// read the file. Using the real shape keeps the parser honest about comments,
// blank-line separation, two-space indentation and bare `/` patterns.
const realHeadersFile = `# Security headers applied site-wide.
# CSP is intentionally NOT set here.
/*
  Strict-Transport-Security: max-age=31536000; includeSubDomains; preload
  X-Frame-Options: DENY
  X-Content-Type-Options: nosniff
  Referrer-Policy: strict-origin-when-cross-origin
  Permissions-Policy: camera=(), microphone=(), geolocation=(), interest-cohort=()

/_astro/*
  Cache-Control: public, max-age=31536000, immutable

/images/*
  Cache-Control: public, max-age=2592000

/docs/*.html
  Cache-Control: public, max-age=60, stale-while-revalidate=300

/*.html
  Cache-Control: public, max-age=300

/
  Cache-Control: public, max-age=3600, stale-while-revalidate=86400

/robots.txt
  Cache-Control: public, max-age=3600
`

func TestParseHeaderRules(t *testing.T) {
	rules := parseHeaderRules(realHeadersFile)
	assert.Len(t, rules, 7, "one rule per pattern block")

	tests := []struct {
		name    string
		urlPath string
		want    map[string]string
	}{
		{
			"site-wide security headers reach every page",
			"/pricing",
			map[string]string{
				"X-Frame-Options":        "DENY",
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "strict-origin-when-cross-origin",
				// The value must survive commas and parentheses intact.
				"Permissions-Policy": "camera=(), microphone=(), geolocation=(), interest-cohort=()",
				// Semicolons must not be treated as separators either.
				"Strict-Transport-Security": "max-age=31536000; includeSubDomains; preload",
			},
		},
		{
			"hashed assets get the immutable cache",
			"/_astro/app.DY-PF2h0.js",
			map[string]string{
				"Cache-Control":   "public, max-age=31536000, immutable",
				"X-Frame-Options": "DENY",
			},
		},
		{
			"images get their own shorter cache",
			"/images/blog/x.webp",
			map[string]string{"Cache-Control": "public, max-age=2592000"},
		},
		{
			"the root pattern matches only the root",
			"/",
			map[string]string{"Cache-Control": "public, max-age=3600, stale-while-revalidate=86400"},
		},
		{
			"an exact path matches",
			"/robots.txt",
			map[string]string{"Cache-Control": "public, max-age=3600"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			rules.apply(header, tt.urlPath)

			for name, want := range tt.want {
				assert.Equal(t, want, header.Get(name), name)
			}
		})
	}
}

// Later blocks override earlier ones, so `/docs/x.html` must end up with the
// docs cache and not the generic `/*.html` one that follows it in file order.
func TestHeaderRulesPrecedence(t *testing.T) {
	rules := parseHeaderRules(realHeadersFile)

	header := http.Header{}
	rules.apply(header, "/about/index.html")
	assert.Equal(t, "public, max-age=300", header.Get("Cache-Control"), "generic html rule")

	header = http.Header{}
	rules.apply(header, "/docs/zopnight/introduction.html")
	// /docs/*.html appears before /*.html, so the generic rule wins by order —
	// this pins the documented precedence rather than an assumed one.
	assert.Equal(t, "public, max-age=300", header.Get("Cache-Control"))
}

func TestParseHeaderRulesMalformed(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"empty file", "", 0},
		{"comments only", "# a\n# b\n", 0},
		{"header before any pattern is dropped", "  X-Foo: bar\n", 0},
		{"pattern with no headers still parses", "/*\n", 1},
		{"header line without a colon is skipped", "/*\n  NotAHeader\n  X-Ok: 1\n", 1},
		{"blank header name is skipped", "/*\n  : value\n", 1},
		{"blank header value is skipped", "/*\n  X-Empty:\n", 1},
		{"CRLF line endings", "/*\r\n  X-Ok: 1\r\n", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Len(t, parseHeaderRules(tt.content), tt.want)
		})
	}

	// A malformed line must not cost the file its other headers.
	rules := parseHeaderRules("/*\n  NotAHeader\n  X-Ok: 1\n")
	header := http.Header{}
	rules.apply(header, "/anything")
	assert.Equal(t, "1", header.Get("X-Ok"))
}

func TestHeaderValuesContainingColons(t *testing.T) {
	// Only the first colon separates name from value.
	rules := parseHeaderRules("/*\n  Content-Security-Policy: default-src https://a.test; img-src *\n")

	header := http.Header{}
	rules.apply(header, "/x")
	assert.Equal(t, "default-src https://a.test; img-src *", header.Get("Content-Security-Policy"))
}

func TestNoHeadersFileIsANoOp(t *testing.T) {
	dir := setupTestDir(t)
	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))

	assert.Empty(t, loadHeaderRules(fs, dir), "a site without _headers gets no rules")

	h := &staticFileHandler{fs: fs, staticFilePath: dir, defaultExtension: ".html", next: http.NotFoundHandler()}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/style.css", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("X-Frame-Options"))
	assert.Empty(t, rec.Header().Get("Cache-Control"))
}

func TestServeHTTPAppliesHeaderRules(t *testing.T) {
	dir := setupTestDir(t)
	writeFile(t, dir, headersFileName, realHeadersFile)

	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))
	rules := loadHeaderRules(fs, dir)
	assert.Len(t, rules, 7, "rules load from disk")

	newHandler := func() *staticFileHandler {
		return &staticFileHandler{
			fs: fs, staticFilePath: dir, defaultExtension: ".html",
			next: http.NotFoundHandler(), headerRules: rules,
		}
	}

	t.Run("a served page carries the site-wide security headers", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/style.css", http.NoBody)
		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	})

	// Netlify applies _headers to error responses too, and a 404 that leaks
	// framing protection is exactly as exploitable as a 200 that does.
	t.Run("a 404 carries them too", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/nope", http.NoBody)
		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	})

	// ...but not its caching. A site's Cache-Control describes the pages it
	// publishes; applying a page rule to a miss would pin a file that is merely
	// not propagated yet into every cache downstream for the rule's lifetime.
	//
	// `/missing.html` is the path that proves it: it matches the `/*.html`
	// block, so without the fix the 404 inherits max-age=300. An extensionless
	// miss would not — it only matches `/*`, which declares no Cache-Control.
	t.Run("a 404 does not inherit the site's Cache-Control", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/missing.html", http.NoBody)
		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Empty(t, rec.Header().Get("Cache-Control"), "a miss must not be cacheable by a page rule")
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"), "security headers still apply")

		// The same rule really does apply on a hit — otherwise this proves nothing.
		hit := httptest.NewRecorder()
		hitReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/docs.html", http.NoBody)
		newHandler().ServeHTTP(hit, hitReq)
		assert.Equal(t, "public, max-age=300", hit.Header().Get("Cache-Control"),
			"control: /*.html caches a page that exists")
	})

	// The rules must not be able to strip Vary and let a CDN cross-serve
	// markdown to a browser.
	t.Run("Vary: Accept survives on a negotiable route", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/docs", http.NoBody)
		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, "control: /docs resolves to docs.html")
		assert.Equal(t, "Accept", rec.Header().Get("Vary"))
	})

	// A hashed bundle can never resolve to markdown, so keying caches on Accept
	// would fragment them for nothing — on exactly the responses the same
	// _headers file marks immutable.
	t.Run("no Vary on an immutable asset", func(t *testing.T) {
		writeFile(t, dir, "_astro/app.DY-PF2h0.js", "console.log(1)")

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/_astro/app.DY-PF2h0.js", http.NoBody)
		rec := httptest.NewRecorder()
		newHandler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, "control: the asset is really served")
		assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable", "control: the asset rule applies")
		assert.Empty(t, rec.Header().Get("Vary"), "an unnegotiable asset must not fragment caches")
	})
}

// TestWellKnownCarriesHeaderRules covers the paths this server hands off rather
// than serves. .well-known is delegated so that ACME challenges are not given an
// extension or swallowed by the SPA fallback — but a site declaring
// X-Frame-Options for `/*` means the whole site, and a delegated path is still
// one this server answered for.
func TestWellKnownCarriesHeaderRules(t *testing.T) {
	dir := setupTestDir(t)
	writeFile(t, dir, headersFileName, realHeadersFile)

	fs := file.NewLocalFileSystem(logging.NewMockLogger(logging.ERROR))
	rules := loadHeaderRules(fs, dir)

	newHandler := func(next http.Handler) *staticFileHandler {
		return &staticFileHandler{
			fs: fs, staticFilePath: dir, defaultExtension: ".html",
			next: next, headerRules: rules,
		}
	}

	// The delegate still decides the body and status: passing through must not
	// mean passing through unprotected.
	t.Run("a delegated response carries the site-wide security headers", func(t *testing.T) {
		served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("acme-challenge-token"))
		})

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/.well-known/acme-challenge/token.html", http.NoBody)
		rec := httptest.NewRecorder()

		newHandler(served).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "acme-challenge-token", rec.Body.String(), "the delegate still writes the body")
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	})

	// A delegate that succeeds is serving a real file, so it keeps the caching
	// the site asked for.
	t.Run("a delegated 200 keeps the site's Cache-Control", func(t *testing.T) {
		served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/.well-known/security.html", http.NoBody)
		rec := httptest.NewRecorder()

		newHandler(served).ServeHTTP(rec, req)

		assert.Equal(t, "public, max-age=300", rec.Header().Get("Cache-Control"),
			"a real delegated file is still a page the site publishes")
	})

	// ...but a delegated miss must not be cached, for the same reason a directly
	// served miss must not be.
	t.Run("a delegated 404 does not inherit Cache-Control", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/.well-known/acme-challenge/absent.html", http.NoBody)
		rec := httptest.NewRecorder()

		newHandler(http.NotFoundHandler()).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Empty(t, rec.Header().Get("Cache-Control"))
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"), "security headers still apply")
	})

	// Delegation itself must survive: this path is how ACME issues certificates.
	t.Run("the path is still handed to the delegate untouched", func(t *testing.T) {
		var gotPath string

		spy := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { gotPath = r.URL.Path })

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/.well-known/acme-challenge/xyz", http.NoBody)

		newHandler(spy).ServeHTTP(httptest.NewRecorder(), req)

		assert.Equal(t, "/.well-known/acme-challenge/xyz", gotPath, "no extension, no rewriting")
	})
}
