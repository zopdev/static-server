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

	// Set explicitly when serving markdown. Go's built-in MIME table has no
	// entry for .md and a scratch base image has no /etc/mime.types, so
	// http.ServeFile would otherwise sniff the file and label it text/plain.
	markdownContentType = "text/markdown; charset=utf-8"

	defaultQuality = 1.0
)

// acceptEntry is one parsed media range from an Accept header.
type acceptEntry struct {
	mediaType string
	quality   float64
}

// parseAcceptEntry parses a single Accept media range. Entries that are
// malformed, or that carry an unparseable q-value, are reported as unusable
// rather than guessed at.
func parseAcceptEntry(entry string) (acceptEntry, bool) {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(entry))
	if err != nil {
		return acceptEntry{}, false
	}

	quality := defaultQuality

	if raw, ok := params["q"]; ok {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return acceptEntry{}, false
		}

		quality = parsed
	}

	return acceptEntry{mediaType: mediaType, quality: quality}, true
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
// send `text/markdown` today. Quality values are honored, so a client that
// lists markdown below HTML (`text/html, text/markdown;q=0.1`) still gets
// HTML — it expressed a preference and we respect it.
func markdownPreferred(accept string) bool {
	if accept == "" {
		return false
	}

	var markdownQuality, htmlQuality float64

	named := false

	for _, raw := range strings.Split(accept, ",") {
		entry, ok := parseAcceptEntry(raw)
		if !ok {
			continue
		}

		switch entry.mediaType {
		case markdownMediaType, legacyMarkdownMediaType:
			named = true
			markdownQuality = max(markdownQuality, entry.quality)
		case htmlMediaType, xhtmlMediaType:
			htmlQuality = max(htmlQuality, entry.quality)
		}
	}

	return named && markdownQuality > 0 && markdownQuality >= htmlQuality
}
