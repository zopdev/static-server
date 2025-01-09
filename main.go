package main

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"gofr.dev/pkg/gofr"
)

const defaultStaticFilePath = `./static`
const defaultPathFileNotFound = "/404.html"

func main() {
	app := gofr.New()

	staticFilePath := app.Config.GetOrDefault("STATIC_DIR_PATH", defaultStaticFilePath)
	fileNotFoundPath := app.Config.GetOrDefault("FILE_NOT_FOUND_PATH", defaultPathFileNotFound)

	app.UseMiddleware(func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			filePath := filepath.Join(staticFilePath, r.URL.Path)

			// check if the path has a file extension
			ok, _ := regexp.MatchString(`\.\S+$`, filePath)

			if r.URL.Path == "/" {
				filePath = "/index.html"
			} else if !ok {
				if stat, err := os.Stat(filePath); err == nil && stat.IsDir() {
					filePath = filepath.Join(r.URL.Path, "/index.html")
				} else {
					r.URL.Path += ".html"
				}
			}

			filePath = filepath.Join(staticFilePath, r.URL.Path)
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				r.URL.Path = fileNotFoundPath

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
