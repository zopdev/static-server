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
