package main

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gofr.dev/pkg/gofr"
	"gofr.dev/pkg/gofr/logging"
)

const staticFilePath = `./website`

func main() {
	app := gofr.New()

	files := createListOfFiles(app.Logger())

	app.UseMiddleware(func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// check if the path has a file extension
			ok, _ := regexp.MatchString(`\.\S+$`, r.URL.Path)

			if r.URL.Path != "/" && !ok {
				r.URL.Path += ".html"
			}

			_, ok = files[r.URL.Path]

			if !ok {
				r.URL.Path = "/404.html"
			}

			handler.ServeHTTP(w, r)
		})
	})

	app.AddStaticFiles("/", staticFilePath)

	app.Run()
}

func createListOfFiles(logger logging.Logger) map[string]bool {
	files := make(map[string]bool)

	files["/"] = true

	_, err := os.Stat(staticFilePath)
	if err != nil {
		logger.Fatalf("Error while reading static files directory %v", err)

		return files
	}

	err = filepath.Walk(staticFilePath, func(path string, info fs.FileInfo, _ error) error {
		after, _ := strings.CutPrefix(path, "website")

		if !info.IsDir() {
			files[after] = true
		}

		return nil
	})
	if err != nil {
		logger.Errorf("Error while walking through static files directory %v", err)

		return files
	}

	logger.Infof("File reading successful")

	for k, _ := range files {
		logger.Infof("File: %v", k)
	}

	return files
}
