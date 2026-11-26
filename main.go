package main

import (
	"net/http"
	"strconv"
	"time"

	"gofr.dev/pkg/gofr"
	"gofr.dev/pkg/gofr/datasource/file"
	"gofr.dev/pkg/gofr/logging"

	"zop.dev/static-server/internal/config"
)

const defaultStaticFilePath = `./static`
const indexHTML = "/index.html"
const htmlExtension = ".html"
const rootPath = "/"

// configLookup is the slice of gofr's config this server reads. Declaring it
// here lets the empty-value resolution below be exercised directly, rather than
// only through a running app.
type configLookup interface {
	GetOrDefault(key, fallback string) string
}

// resolveOrDefault returns the configured value for key, falling back when the
// key is absent *or* present but empty.
//
// gofr's GetOrDefault only covers absent. The shipped configs/.env sets these
// keys with empty values, so a deployment supplying STATIC_DIR_PATH through the
// environment would otherwise receive "" and silently root every lookup at the
// process working directory — serving pages while loading zero `_headers` rules,
// the kind of half-working state that never gets noticed.
func resolveOrDefault(cfg configLookup, key, fallback string) string {
	if value := cfg.GetOrDefault(key, fallback); value != "" {
		return value
	}

	return fallback
}

func main() {
	app := gofr.New()

	staticFilePath := resolveOrDefault(app.Config, "STATIC_DIR_PATH", defaultStaticFilePath)

	// SPA_MODE needs no such guard: ParseBool rejects "" and leaves the same
	// false the default would have produced.
	spaMode, _ := strconv.ParseBool(app.Config.GetOrDefault("SPA_MODE", "false"))

	defaultExtension := resolveOrDefault(app.Config, "DEFAULT_EXTENSION", htmlExtension)

	handler := &staticFileHandler{
		staticFilePath:   staticFilePath,
		spaMode:          spaMode,
		defaultExtension: defaultExtension,
	}

	app.OnStart(func(ctx *gofr.Context) error {
		if err := config.HydrateFile(ctx.File, app.Config); err != nil {
			ctx.Logger.Error(err.Error())
		}

		handler.loadSite(ctx.File, ctx.Logger)

		return nil
	})

	app.UseMiddleware(func(next http.Handler) http.Handler {
		handler.next = next
		return http.HandlerFunc(handler.ServeHTTP)
	})

	app.AddStaticFiles("/", staticFilePath)

	app.Run()
}

// loadSite does the per-site work that happens once at startup: it points the
// handler at fs, reads the `_headers` rules, and indexes precompressed
// sidecars. It lives outside the OnStart closure so a test can prove the
// handler is actually wired with both, not only that each loader works.
func (h *staticFileHandler) loadSite(fs file.FileSystem, logger logging.Logger) {
	h.fs = fs

	// Read once at startup rather than per request. Absent file → no rules
	// → responses are byte-for-byte what they are today.
	h.headerRules = loadHeaderRules(fs, h.staticFilePath)
	// The resolved directory is logged with the count: "0 rules" is normal
	// for a site without the file, but indistinguishable from a misrooted
	// path unless the path is on the line too.
	logger.Infof("loaded %d %s rule(s) from %s", len(h.headerRules), headersFileName, h.staticFilePath)

	// Sidecars are indexed once, here, so a request never probes the disk
	// for them. A partial failure keeps what was indexed; startup never
	// fails on it (gofr exits the app when an OnStart hook errors), since
	// an empty index only means plain files, exactly as before.
	started := time.Now()

	sidecars, err := buildSidecarIndex(fs, h.staticFilePath)
	if err != nil {
		logger.Errorf("indexing precompressed sidecars under %s: %v", h.staticFilePath, err)
	}

	h.sidecars = sidecars
	logger.Infof("indexed %d precompressed sidecar(s) in %s from %s",
		sidecars.size(), time.Since(started).Round(time.Millisecond), h.staticFilePath)
}
