package main

import (
	"mime"
	"strconv"
	"strings"
)

const (
	markdownExtension = ".md"
	markdownMediaType = "text/markdown"
	// Some clients still send the pre-RFC-7763 spelling.
	legacyMarkdownMediaType = "text/x-markdown"
	htmlMediaType           = "text/html"
	xhtmlMediaType          = "application/xhtml+xml"
)

// Go's built-in MIME table has no entry for .md, and a scratch/distroless
// image has no /etc/mime.types to fall back on, so http.ServeFile would sniff
// the file and label it text/plain. Register it explicitly so the content type
// is correct regardless of what the base image ships.
func init() {
	_ = mime.AddExtensionType(markdownExtension, "text/markdown; charset=utf-8")
}

// markdownPreferred reports whether the client asked for markdown in
// preference to HTML.
//
// "Asked for" means named the type. Browsers send
// `text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8` — that
// trailing wildcard technically matches text/markdown, so matching on
// wildcards would serve raw markdown to every browser on the internet. Only an
// explicit media type counts.
//
// Agents that want markdown do name it: Claude Code, Cursor and OpenCode all
// send `text/markdown` today. Quality values are honoured, so a client that
// lists markdown below HTML (`text/html, text/markdown;q=0.1`) still gets
// HTML — it expressed a preference and we respect it.
func markdownPreferred(accept string) bool {
	if accept == "" {
		return false
	}

	var markdownQ, htmlQ float64

	named := false

	for _, entry := range strings.Split(accept, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(entry))
		if err != nil {
			continue
		}

		q := 1.0

		if raw, ok := params["q"]; ok {
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				continue
			}

			q = parsed
		}

		switch mediaType {
		case markdownMediaType, legacyMarkdownMediaType:
			named = true

			if q > markdownQ {
				markdownQ = q
			}
		case htmlMediaType, xhtmlMediaType:
			if q > htmlQ {
				htmlQ = q
			}
		}
	}

	return named && markdownQ > 0 && markdownQ >= htmlQ
}
