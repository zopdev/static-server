package main

import (
	"net/http"
	"strconv"

	"gofr.dev/pkg/gofr"

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
		handler.fs = ctx.File

		if err := config.HydrateFile(ctx.File, app.Config); err != nil {
			ctx.Logger.Error(err.Error())
		}

		// Read once at startup rather than per request. Absent file → no rules
		// → responses are byte-for-byte what they are today.
		handler.headerRules = loadHeaderRules(ctx.File, staticFilePath)
		// The resolved directory is logged with the count: "0 rules" is normal
		// for a site without the file, but indistinguishable from a misrooted
		// path unless the path is on the line too.
		ctx.Logger.Infof("loaded %d %s rule(s) from %s", len(handler.headerRules), headersFileName, staticFilePath)

		return nil
	})

	app.UseMiddleware(func(next http.Handler) http.Handler {
		handler.next = next
		return http.HandlerFunc(handler.ServeHTTP)
	})

	app.AddStaticFiles("/", staticFilePath)

	app.Run()
}
