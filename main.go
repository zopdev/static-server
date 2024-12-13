package main

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"gofr.dev/pkg/gofr"
)

const defaultStaticFilePath = `./static`

func main() {
	app := gofr.New()

	staticFilePath := app.Config.GetOrDefault("STATIC_DIR_PATH", defaultStaticFilePath)

	app.UseMiddleware(func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// check if the path has a file extension
			ok, _ := regexp.MatchString(`\.\S+$`, r.URL.Path)

			if r.URL.Path != "/" && !ok {
				r.URL.Path += ".html"
			}

			filePath := filepath.Join(staticFilePath, r.URL.Path)
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				r.URL.Path = "/404.html"

				w.WriteHeader(http.StatusNotFound)

				filePath = filepath.Join(staticFilePath, "404.html")

				http.ServeFile(w, r, filePath)

				return
			}

			http.ServeFile(w, r, filePath)
		})
	})

	app.AddStaticFiles("/", staticFilePath)

	app.Run()
}
