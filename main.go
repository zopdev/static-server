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

// sanitizePath validates the requested path is within the static directory.
// Returns the sanitized absolute path and true if valid, empty string and false otherwise.
func sanitizePath(staticFilePath, requestPath string) (string, bool) {
	filePath := filepath.Join(staticFilePath, filepath.Clean("/"+requestPath))

	absStaticPath, err := filepath.Abs(staticFilePath)
	if err != nil {
		return "", false
	}

	absFilePath, err := filepath.Abs(filePath)
	if err != nil || !strings.HasPrefix(absFilePath, absStaticPath) {
		return "", false
	}

	return filePath, true
}

// resolveFilePath determines the actual file path to serve based on the request.
func resolveFilePath(filePath, requestPath string) string {
	// check if the path has a file extension
	ok, _ := regexp.MatchString(`\.\S+$`, filePath)

	if requestPath == rootPath {
		return filePath + indexHTML
	}

	if !ok {
		if _, err := os.Stat(filePath + ".html"); err == nil {
			return filePath + htmlExtension
		}

		if stat, err := os.Stat(filePath); err == nil && stat.IsDir() {
			return filePath + indexHTML
		}
	}

	return filePath
}

func main() {
	app := gofr.New()

	staticFilePath := app.Config.GetOrDefault("STATIC_DIR_PATH", defaultStaticFilePath)

	app.UseMiddleware(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/.well-known/") {
				h.ServeHTTP(w, r)

				return
			}

			filePath, ok := sanitizePath(staticFilePath, r.URL.Path)
			if !ok {
				w.WriteHeader(http.StatusForbidden)
				return
			}

			filePath = resolveFilePath(filePath, r.URL.Path)

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
