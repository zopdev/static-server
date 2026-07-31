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
}

func (h *staticFileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/.well-known/") {
		h.next.ServeHTTP(w, r)

		return
	}

	filePath, hasExtension := h.resolveFilePath(r.URL.Path, r.Header.Get("Accept"))

	// The response body for a given URL now depends on Accept, so caches must
	// key on it. Without this a CDN can hand an agent's markdown response to
	// the next browser that asks for the same page.
	w.Header().Add("Vary", "Accept")

	if _, err := h.fs.Stat(filePath); err != nil {
		if h.spaMode && !hasExtension {
			http.ServeFile(w, r, filepath.Join(h.staticFilePath, indexHTML))
			return
		}

		http.ServeFile(&statusOverrideWriter{ResponseWriter: w, status: http.StatusNotFound}, r,
			filepath.Join(h.staticFilePath, "404.html"))

		return
	}

	http.ServeFile(w, r, filePath)
}

func (h *staticFileHandler) resolveFilePath(urlPath, accept string) (string, bool) {
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
	if !hasExtension && urlPath != rootPath && markdownPreferred(accept) {
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

type statusOverrideWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusOverrideWriter) WriteHeader(int) {
	w.ResponseWriter.WriteHeader(w.status)
}
