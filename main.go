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

	staticFilePath := app.Config.GetOrDefault("STATIC_DIR_PATH", defaultStaticFilePath)
	spaMode, _ := strconv.ParseBool(app.Config.GetOrDefault("SPA_MODE", "false"))

	defaultExtension := app.Config.GetOrDefault("DEFAULT_EXTENSION", htmlExtension)

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
		ctx.Logger.Infof("loaded %d %s rule(s)", len(handler.headerRules), headersFileName)

		return nil
	})

	app.UseMiddleware(func(next http.Handler) http.Handler {
		handler.next = next
		return http.HandlerFunc(handler.ServeHTTP)
	})

	app.AddStaticFiles("/", staticFilePath)

	app.Run()
}
