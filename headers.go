package main

import (
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"gofr.dev/pkg/gofr/datasource/file"
)

// headersFileName is the Netlify / Cloudflare Pages convention: a `_headers`
// file at the root of the published directory, listing path patterns and the
// response headers to send for them.
//
// Static site generators emit this file expecting the host to honor it. A
// host that ignores it fails silently and in the worst way — the file looks
// authoritative in the repo while the headers it declares were never sent. On
// zop.dev the entire block (X-Frame-Options, X-Content-Type-Options,
// Referrer-Policy, Permissions-Policy, and every Cache-Control rule including
// `immutable` on hashed assets) had never once reached a browser.
const headersFileName = "_headers"

// headerPair is a single `Name: value` line.
type headerPair struct {
	name  string
	value string
}

// headerRule is one block: a path pattern and the headers it contributes.
type headerRule struct {
	match   *regexp.Regexp
	headers []headerPair
}

// headerRules is the parsed file, in source order.
type headerRules []headerRule

// apply writes every header whose pattern matches urlPath. All matching rules
// contribute, in file order, so a later specific block overrides an earlier
// catch-all — the same precedence the format has on Netlify and Cloudflare.
func (r headerRules) apply(header http.Header, urlPath string) {
	for i := range r {
		if !r[i].match.MatchString(urlPath) {
			continue
		}

		for _, pair := range r[i].headers {
			header.Set(pair.name, pair.value)
		}
	}
}

// patternToRegexp converts a `_headers` path pattern to an anchored regexp.
// `*` matches any run of characters including `/`, matching the upstream
// behavior where `/*` covers the whole site.
func patternToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder

	b.WriteString("^")

	for i, part := range strings.Split(pattern, "*") {
		if i > 0 {
			b.WriteString(".*")
		}

		b.WriteString(regexp.QuoteMeta(part))
	}

	b.WriteString("$")

	return regexp.Compile(b.String())
}

// isPatternLine reports whether a raw line opens a new block. Patterns sit at
// column zero; header lines are indented, which is what separates them.
func isPatternLine(raw string) bool {
	return strings.HasPrefix(raw, "/")
}

// parseHeaderRules parses `_headers` content. Unparseable lines are skipped
// rather than failing the whole file: a single malformed rule should not cost
// a site every other header it declares.
func parseHeaderRules(content string) headerRules {
	var rules headerRules

	for _, raw := range strings.Split(content, "\n") {
		raw = strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(raw)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if isPatternLine(raw) {
			match, err := patternToRegexp(trimmed)
			if err != nil {
				continue
			}

			rules = append(rules, headerRule{match: match})

			continue
		}

		if len(rules) == 0 {
			continue
		}

		name, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}

		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)

		if name == "" || value == "" {
			continue
		}

		last := len(rules) - 1
		rules[last].headers = append(rules[last].headers, headerPair{name: name, value: value})
	}

	return rules
}

// loadHeaderRules reads `_headers` from the published directory. A missing or
// unreadable file yields no rules, so a deployment without one behaves exactly
// as it does today.
func loadHeaderRules(fs file.FileSystem, staticFilePath string) headerRules {
	f, err := fs.Open(filepath.Join(staticFilePath, headersFileName))
	if err != nil {
		return nil
	}

	defer func() { _ = f.Close() }()

	content, err := io.ReadAll(f)
	if err != nil {
		return nil
	}

	return parseHeaderRules(string(content))
}
