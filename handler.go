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

	wantsMarkdown := markdownPreferred(r.Header.Get("Accept"))

	filePath, hasExtension := h.resolveFilePath(r.URL.Path, wantsMarkdown)

	// The response body for a given URL now depends on Accept, so caches must
	// key on it. Without this a CDN can hand an agent's markdown response to
	// the next browser that asks for the same page.
	w.Header().Add("Vary", "Accept")

	if _, err := h.fs.Stat(filePath); err != nil {
		if h.spaMode && !hasExtension {
			http.ServeFile(w, r, filepath.Join(h.staticFilePath, indexHTML))
			return
		}

		// A client that asked for markdown cannot use an HTML error shell —
		// and those shells are not small. The 404 page of a real site measured
		// 144 KB, sent in reply to a request the client could not parse.
		// Answer in the type it asked for, at a size that suits an error.
		if wantsMarkdown {
			writeMarkdownNotFound(w)
			return
		}

		http.ServeFile(&statusOverrideWriter{ResponseWriter: w, status: http.StatusNotFound}, r,
			filepath.Join(h.staticFilePath, "404.html"))

		return
	}

	// http.ServeFile only sniffs a Content-Type when one is not already set,
	// so setting it here wins. Applies to negotiated and directly-requested
	// .md alike — neither can rely on the base image having /etc/mime.types.
	if strings.HasSuffix(filePath, markdownExtension) {
		w.Header().Set("Content-Type", markdownContentType)
	}

	http.ServeFile(w, r, filePath)
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
	if !hasExtension && urlPath != rootPath && wantsMarkdown {
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
