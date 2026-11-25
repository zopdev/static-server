# Static Server

A lightweight static file server built on [GoFr](https://gofr.dev), designed for containerized deployment of static websites and SPAs.

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `STATIC_DIR_PATH` | `./static` | Path to the directory containing static files |
| `SPA_MODE` | `false` | Serve `index.html` for extensionless routes that don't match a file |
| `DEFAULT_EXTENSION` | `.html` | Single extension appended when the URL has none (e.g. `/docs` → `docs<ext>`). Only this one extension is tried — setting it to a non-`.html` value disables `.html` auto-resolution. |
| `CONFIG_FILE_PATH` | *(empty)* | Path to a config file for `${VAR}` placeholder hydration at startup |
| `HTTP_PORT` | `8000` | Port the server listens on |

> **`DEFAULT_EXTENSION` + `SPA_MODE`:** path resolution only tries one extension. If you set `DEFAULT_EXTENSION=.json`, a request for `/docs` looks for `docs.json` only — `docs.html` will not be found, and with `SPA_MODE=true` the request falls through to `index.html`. Leave `DEFAULT_EXTENSION=.html` unless every extensionless route on your site resolves to the same non-html file type.

> A variable that is *set but empty* counts as unset and falls back to its default. The shipped `configs/.env` declares `STATIC_DIR_PATH=` and `DEFAULT_EXTENSION=` with empty values, so supplying them through the environment would otherwise resolve to `""`.

## Content Negotiation

A request whose `Accept` header names `text/markdown` is served the markdown sibling of the page, when one exists: `/about` returns `about.md` instead of `about/index.html`. Static site generators already emit these files, so **no build change is needed** — if the `.md` isn't there, the request falls through to HTML untouched.

This exists because agents otherwise have to download the whole HTML document to discover the `<link rel="alternate">` inside it. Measured on a real site, the markdown ran 15–96× smaller than the page it replaces.

| Behavior | Detail |
|---|---|
| What counts as asking | The media type must be **named**. Browsers send `*/*;q=0.8`, which matches `text/markdown` by the letter of RFC 9110 — matching wildcards would serve raw source to every visitor. `text/x-markdown` is accepted too. |
| Preference is respected | `q`-values are honored: `text/html, text/markdown;q=0.1` still gets HTML. |
| Which URLs negotiate | Extensionless routes only. `/` is always `index.html`, and a path with an extension (`/style.css`, `/about.md`) resolves the same way for every client. |
| `Vary: Accept` | Set on responses that can depend on `Accept` — negotiable routes, the SPA fallback for them, and every miss. **Not** set on assets, so a CDN isn't asked to fragment its cache on a header that cannot change what it returns. A `Vary` your `_headers` declares is preserved, not replaced. |
| Misses | A client that asked for markdown gets a small markdown 404 naming `/sitemap.xml` and `/llms.txt`, rather than an HTML error shell it cannot parse. |
| `Content-Type` | Set explicitly for a **negotiated** response, since the distroless base image has no `/etc/mime.types`. A directly requested `.md` is left as-is, so existing links to `.md` files behave exactly as before. |

## Response Headers (`_headers`)

If the published directory contains a `_headers` file — the [Netlify](https://docs.netlify.com/routing/headers/) / Cloudflare Pages convention — its rules are parsed once at startup and applied to matching responses. **No file means no rules and no change**, so this is inert for sites that don't ship one.

```
/*
  X-Frame-Options: DENY
  X-Content-Type-Options: nosniff

/_astro/*
  Cache-Control: public, max-age=31536000, immutable
```

| Behavior | Detail |
|---|---|
| Matching | `*` matches any run of characters, including `/`. Patterns are anchored, so `/docs/*` does not match `/other/docs/x`. Netlify's `:placeholder` syntax is **not** supported. |
| Precedence | Every matching rule contributes, in file order, so a later specific block overrides an earlier catch-all. |
| Malformed lines | Skipped individually — one bad rule doesn't cost the file its other headers. |
| Errors and misses | Rules apply to 404s and the SPA fallback too: a 404 that leaks framing protection is as exploitable as a 200 that does. **`Cache-Control` is the exception** — it is withdrawn from a miss, and from a delegated response that returns 4xx/5xx, so a `/*` cache rule cannot pin a file that is merely un-propagated mid-deploy. The SPA fallback keeps its caching, being a real route rather than a miss. |
| `/.well-known/` | Delegated to the framework so ACME challenges resolve untouched, but the header rules still apply to it. |

> **Reverse proxies win.** If something in front of this server sets the same header, its value is what reaches the client — ingress-nginx sends `Strict-Transport-Security` by default, for instance. Headers with no proxy counterpart take effect immediately.

The startup log names the resolved directory alongside the rule count, since `0 rules` is normal for a site without the file and otherwise indistinguishable from a misrooted path.

## Precompressed Files

When the build ships a compressed copy of a file next to it — `app.js.br` (Brotli) or `app.js.gz` (gzip) beside `app.js` — and the client's `Accept-Encoding` allows it, the server sends that copy with `Content-Encoding` set, instead of the original. Nothing is compressed at request time.

**Sidecars must be generated by the site build.** The server only serves what is on disk: a site that ships no `.br` / `.gz` files gets exactly the responses it got before this feature existed, header for header.

| Behavior | Detail |
|---|---|
| Preference | **Brotli wins over gzip whenever the client accepts both, whatever q-values it sends.** q-values decide only whether a coding is acceptable, not which acceptable one is chosen. A gzip sidecar is used when br is not acceptable or has no sidecar. |
| `Accept-Encoding` parsing | Case-insensitive, whitespace-tolerant, repeated header lines combined. `q=0` refuses a coding; `*` covers codings not named; a named coding overrides `*`. A malformed weight (`q=abc`, `q=2`) makes that coding **not acceptable**, and if a coding is both accepted and refused in one request, the refusal wins. Nothing is ever answered with 406: the fallback is always the original file. |
| `Content-Type` | Taken from the **original** file, never the sidecar: by extension, or by sniffing the original's bytes where the MIME table has no entry (as for `.md` on the distroless image). A `Content-Type` from `_headers`, or the negotiated markdown type, still wins. |
| `_headers` | Applied to encoded responses exactly as to plain ones. |
| `Vary: Accept-Encoding` | Added only for files that have a sidecar, on both the encoded and the plain response, so a shared cache keys on it. Added alongside any `Vary` the site declares, never replacing it. |
| Conditional requests | `Last-Modified` is the original file's, so `If-Modified-Since` gets the same 304-or-200 answer whatever the encoding. A 304 still carries `Vary`. |
| `Range` | Ignored on an encoded response: the full encoded body is sent with 200 and no `Accept-Ranges`. A client that does not accept the encoding gets range support as before. |
| `HEAD` | Reports the encoded `Content-Length`. |
| Markdown negotiation | Covered: an `Accept: text/markdown` request served `about.md` uses `about.md.br`, and keeps `Content-Type: text/markdown`. |
| Not covered | The 404 page and the SPA fallback are always sent uncompressed. URLs ending `/index.html` keep their redirect. A direct request for `/app.js.br` is served as a plain file, as before. An orphan sidecar (`x.br` with no `x`) never makes `/x` resolve. |

> Sidecars are trusted as-is: the build must regenerate them whenever the original changes, and each must decode to its original byte for byte. A stale or truncated sidecar is served to every client that accepts its encoding.

## Usage

### Docker

```dockerfile
FROM zopdev/static-server:v0.0.9

# Copy static files (must use --chown for nonroot user)
COPY --chown=nonroot:nonroot ./build /static

# Optional: enable SPA mode for client-side routing
ENV SPA_MODE=true

# Optional: hydrate config file with env vars at startup
ENV CONFIG_FILE_PATH=/static/config.json

CMD ["/main"]
```

```bash
docker build -t my-app .
docker run -d -p 8000:8000 my-app
```

> **Note:** Docker volume mounts (`-v`) are not supported. The image runs as `nonroot:nonroot`, and mounted volumes are typically owned by `root`. Use `COPY --chown=nonroot:nonroot` instead.

### Without Docker

```bash
STATIC_DIR_PATH=./my-site ./main
```

## Config File Hydration

When `CONFIG_FILE_PATH` is set, the server replaces `${VAR}` placeholders in that file at startup using environment variables. The file is rewritten in-place before serving begins.

This is useful for injecting runtime configuration (API URLs, client IDs, etc.) into static front-end apps without rebuilding them.

**Example** &mdash; given `config.json`:
```json
{ "clientId": "${GOOGLE_CLIENT_ID}", "apiUrl": "${API_BASE_URL}" }
```
With `GOOGLE_CLIENT_ID=abc123` and `API_BASE_URL=https://api.example.com`, the file becomes:
```json
{ "clientId": "abc123", "apiUrl": "https://api.example.com" }
```

If any placeholders have no matching variable, empty strings are substituted and an error is logged.

> The config file must be writable by `nonroot`. Use `COPY --chown=nonroot:nonroot` in your Dockerfile.

## License

MIT &mdash; see [LICENSE](LICENSE).
