package main

import (
	"net/http"
	"path/filepath"
	"strings"

	"gofr.dev/pkg/gofr/datasource/file"
)

type staticFileHandler struct {
	fs               file.FileSystem
	staticFilePath   string
	spaMode          bool
	defaultExtension string
	next             http.Handler

	// Parsed once at startup from `_headers`; nil when the site has no such
	// file, in which case nothing about the response changes.
	headerRules headerRules
}

func (h *staticFileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Applied before anything writes, so it covers hits, misses, the SPA
	// fallback and the delegated paths below alike. Set first so the server's
	// own headers further down still win where they are load-bearing.
	h.headerRules.apply(w.Header(), r.URL.Path)

	// .well-known is handed off untouched — ACME challenges and the like must
	// not pick up an extension or the SPA fallback. The site's headers still
	// apply to it: a `/*` block declaring X-Frame-Options means the whole site,
	// and a path this server delegates is still a path it answers for.
	//
	// The status is not known here, because the delegate chooses it, so the
	// cache directives are withdrawn on the way out instead of up front.
	if strings.Contains(r.URL.Path, "/.well-known/") {
		h.next.ServeHTTP(&errorCacheScrubber{ResponseWriter: w}, r)

		return
	}

	wantsMarkdown := markdownPreferred(r.Header.Get("Accept"))

	filePath, hasExtension := h.resolveFilePath(r.URL.Path, wantsMarkdown)

	if _, err := h.fs.Stat(filePath); err != nil {
		if h.spaMode && !hasExtension {
			// The shell answers a route an agent may instead have been handed a
			// .md sibling for: with `foo.md` on disk but no HTML page, markdown
			// clients take the hit path above while browsers land here, so one
			// URL yields two bodies. Saying so is what stops a shared cache
			// handing the shell to the next client that asked for markdown.
			if negotiable(r.URL.Path) {
				advertiseAcceptVaries(w.Header())
			}

			http.ServeFile(w, r, filepath.Join(h.staticFilePath, indexHTML))

			return
		}

		h.serveNotFound(w, r, wantsMarkdown)

		return
	}

	// Only a negotiable route can resolve to a .md sibling, so only there can
	// the body depend on Accept. Advertising Vary on everything else would
	// fragment caches on a header that cannot change what they return — and
	// those are exactly the hashed assets `_headers` marks immutable, so the
	// cost would land on the responses this server most wants cached.
	if negotiable(r.URL.Path) {
		advertiseAcceptVaries(w.Header())
	}

	// http.ServeFile only sniffs a Content-Type when one is not already set,
	// so setting it here wins. See labelAsMarkdown for why this is scoped to a
	// negotiated response.
	if labelAsMarkdown(wantsMarkdown, r.URL.Path, filePath) {
		w.Header().Set("Content-Type", markdownContentType)
	}

	http.ServeFile(w, r, filePath)
}

// serveNotFound answers a miss, in the type the client asked for.
func (h *staticFileHandler) serveNotFound(w http.ResponseWriter, r *http.Request, wantsMarkdown bool) {
	// A miss is answered in markdown whenever the client asked for it, so this
	// response depends on Accept whatever the path looks like — including the
	// extensions that never negotiate on a hit.
	advertiseAcceptVaries(w.Header())

	withdrawCacheDirectives(w.Header())

	// A client that asked for markdown cannot use an HTML error shell — and
	// those shells are not small. The 404 page of a real site measured 144 KB,
	// sent in reply to a request the client could not parse. Answer in the type
	// it asked for, at a size that suits an error.
	if wantsMarkdown {
		writeMarkdownNotFound(w)
		return
	}

	http.ServeFile(&statusOverrideWriter{ResponseWriter: w, status: http.StatusNotFound}, r,
		filepath.Join(h.staticFilePath, "404.html"))
}

// The requested path is deliberately not echoed back. Reflecting a
// caller-controlled string into a response body is an injection sink even at
// text/markdown, and the caller already knows which URL it asked for — the
// recovery pointers are the part it does not have.
const notFoundMarkdown = "# 404 Not Found\n\n" +
	"The requested page does not exist on this server.\n\n" +
	"See /sitemap.xml for the pages that do, or /llms.txt for an overview.\n"

// writeMarkdownNotFound answers a miss in markdown and points the reader at
// the two files that let it recover on its own rather than guessing at URLs.
func writeMarkdownNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", markdownContentType)
	w.WriteHeader(http.StatusNotFound)

	_, _ = w.Write([]byte(notFoundMarkdown))
}

func (h *staticFileHandler) resolveFilePath(urlPath string, wantsMarkdown bool) (string, bool) {
	filePath := filepath.Join(h.staticFilePath, urlPath)

	hasExtension := filepath.Ext(filePath) != ""

	// Markdown content negotiation. Static site generators emit the markdown
	// source of a page as a sibling of its directory index — `about.md` next
	// to `about/index.html` — so when a client explicitly asks for markdown we
	// can serve that file with no build changes and no extra round trip.
	//
	// This matters because the alternative costs the agent the whole HTML
	// document first: it can only learn a .md exists by reading the
	// <link rel="alternate"> inside the page it was trying to avoid
	// downloading. On zop.dev the same page is 15-60x smaller as markdown.
	//
	// Falls through untouched when the client didn't ask or the file isn't
	// there, so nothing an existing deployment serves today can change.
	if wantsMarkdown && negotiable(urlPath) {
		if _, err := h.fs.Stat(filePath + markdownExtension); err == nil {
			return filePath + markdownExtension, true
		}
	}

	if urlPath == rootPath {
		filePath += indexHTML
	} else if !hasExtension {
		if _, err := h.fs.Stat(filePath + h.defaultExtension); err == nil {
			filePath += h.defaultExtension
		} else if info, err := h.fs.Stat(filePath); err == nil && info.IsDir() {
			filePath += indexHTML
		}
	}

	return filePath, hasExtension
}

// withdrawCacheDirectives removes the site's Cache-Control from a response that
// turned out not to be a page it publishes.
//
// A `_headers` file describes what a site serves; an error is not that. Letting
// a `/*.html` rule reach a miss would pin a file that is merely un-propagated
// mid-deploy into every cache between this server and the reader for the rule's
// full lifetime. The security headers still apply either way — a 404 that leaks
// framing protection is as exploitable as a 200 that does.
//
// This is the one definition of that rule; both the miss path and the delegated
// .well-known path go through it, so they cannot drift apart.
func withdrawCacheDirectives(header http.Header) {
	header.Del("Cache-Control")
}

// errorCacheScrubber applies withdrawCacheDirectives to a response whose status
// is chosen by a handler this server delegated to, and is therefore not known
// when the `_headers` rules are set.
//
// Deliberately not used on the main serving path: wrapping the writer there
// would hide net/http's io.ReaderFrom from http.ServeFile and cost every static
// file its sendfile fast path. The delegated paths are ACME challenges and the
// like — small, rare, and not worth a special case to keep fast.
type errorCacheScrubber struct {
	http.ResponseWriter
}

func (w *errorCacheScrubber) WriteHeader(status int) {
	if status >= http.StatusBadRequest {
		withdrawCacheDirectives(w.Header())
	}

	w.ResponseWriter.WriteHeader(status)
}

type statusOverrideWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusOverrideWriter) WriteHeader(int) {
	w.ResponseWriter.WriteHeader(w.status)
}
