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

func main() {
	app := gofr.New()

	// GetOrDefault only falls back when the key is absent, so a key present but
	// empty — which the shipped configs/.env has for all four of these — yields
	// an empty path rather than the default. That silently roots every lookup
	// at the process working directory. Treat empty as unset.
	staticFilePath := app.Config.GetOrDefault("STATIC_DIR_PATH", defaultStaticFilePath)
	if staticFilePath == "" {
		staticFilePath = defaultStaticFilePath
	}

	spaMode, _ := strconv.ParseBool(app.Config.GetOrDefault("SPA_MODE", "false"))

	defaultExtension := app.Config.GetOrDefault("DEFAULT_EXTENSION", htmlExtension)
	if defaultExtension == "" {
		defaultExtension = htmlExtension
	}

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
