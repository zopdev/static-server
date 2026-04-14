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

	filePath, hasExtension := h.resolveFilePath(r.URL.Path)

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

func (h *staticFileHandler) resolveFilePath(urlPath string) (string, bool) {
	filePath := filepath.Join(h.staticFilePath, urlPath)

	hasExtension := filepath.Ext(filePath) != ""

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
