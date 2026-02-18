package main

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gofr.dev/pkg/gofr"
)

const defaultStaticFilePath = `./static`
const indexHTML = "/index.html"
const htmlExtension = ".html"
const rootPath = "/"

func main() {
	app := gofr.New()

	staticFilePath := app.Config.GetOrDefault("STATIC_DIR_PATH", defaultStaticFilePath)

	app.UseMiddleware(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/.well-known/") {
				h.ServeHTTP(w, r)

				return
			}

			filePath := filepath.Join(staticFilePath, r.URL.Path)

			// check if the path has a file extension
			ok, _ := regexp.MatchString(`\.\S+$`, filePath)

			if r.URL.Path == rootPath {
				filePath += indexHTML
			} else if !ok {
				if _, err := os.Stat(filePath + ".html"); err == nil {
					filePath += htmlExtension
				} else if stat, err := os.Stat(filePath); err == nil && stat.IsDir() {
					filePath += indexHTML
				}
			}

			if _, err := os.Stat(filePath); os.IsNotExist(err) {
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
