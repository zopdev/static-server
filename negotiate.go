package main

import (
	"mime"
	"net/http"
	"path/filepath"
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

// negotiable reports whether the response for a URL path can depend on Accept.
//
// Only an extensionless route can resolve to a `.md` sibling; the root is
// served from index.html and never negotiates. Everything else — hashed
// bundles, images, a directly requested .md — resolves to the same file
// whatever the client asks for, so its response neither varies nor needs to
// say that it might.
//
// This is the single definition of that condition: resolveFilePath gates
// negotiation on it and the handler gates Vary and the markdown Content-Type
// on it, so the three cannot drift apart.
func negotiable(urlPath string) bool {
	return urlPath != rootPath && filepath.Ext(urlPath) == ""
}

// advertiseAcceptVaries records that this response depends on Accept, so that a
// shared cache keys on it instead of handing one client's copy to another.
//
// Add rather than Set: a site's own `_headers` may declare a Vary of its own,
// and Set would discard it. Repeated Vary field lines are combined by caches, so
// a declared `Vary: Accept-Encoding` plus this one reads as
// `Accept-Encoding, Accept` — which is exactly right. The only case worth
// guarding is a site that already named Accept itself, where adding it again
// would yield a pointless `Accept, Accept`.
func advertiseAcceptVaries(header http.Header) {
	for _, line := range header.Values("Vary") {
		for _, field := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Accept") {
				return
			}
		}
	}

	header.Add("Vary", "Accept")
}

// labelAsMarkdown reports whether this server should set an explicit markdown
// Content-Type rather than leaving the type to http.ServeFile.
//
// Only a negotiated response gets one. ServeFile would otherwise sniff the file
// as text/plain wherever the platform has no `.md` entry — Go's built-in table
// has none and the distroless base image ships no /etc/mime.types, so that is
// the case in production.
//
// A directly requested .md is left alone on purpose: whatever it resolves to
// today is what a site's existing .md links already behave like, and browsers
// render text/plain inline but download text/markdown. Note the starting point
// differs by platform — most Linux distributions do map .md, so there the type
// is already text/markdown and this changes nothing. That is why the decision
// is tested through this function: asserting on a served response would only
// pin whatever the host's MIME table happens to say.
func labelAsMarkdown(wantsMarkdown bool, urlPath, filePath string) bool {
	return wantsMarkdown && negotiable(urlPath) && strings.HasSuffix(filePath, markdownExtension)
}

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
