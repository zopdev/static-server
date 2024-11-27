package main

import (
	"gofr.dev/pkg/gofr"
	"gofr.dev/pkg/gofr/logging"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	app := gofr.New()

	files := createListOfFiles(app.Config.Get("STATIC_DIR_PATH"), app.Logger())

	app.UseMiddleware(func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// check if the path has a file extension
			ok, _ := regexp.MatchString(`\.\S+$`, r.URL.Path)

			if r.URL.Path != "/" && !ok {
				r.URL.Path = r.URL.Path + ".html"
			}

			_, ok = files[r.URL.Path]

			if !ok {
				r.URL.Path = "/404.html"
			}

			handler.ServeHTTP(w, r)
		})
	})

	app.AddStaticFiles("/", "./website")

	app.Run()
}

func createListOfFiles(staticDirPath string, logger logging.Logger) map[string]bool {
	files := make(map[string]bool)

	files["/"] = true

	_, err := os.Stat(staticDirPath)
	if err != nil {
		logger.Errorf("Error while reading static files directory %v", err)

		return files
	}

	err = filepath.Walk(staticDirPath, func(path string, info fs.FileInfo, err error) error {
		after, _ := strings.CutPrefix(path, "website")
		if !info.IsDir() {
			files[after] = true
		}

		return nil
	})
	if err != nil {
		logger.Errorf("Error while walking through static files directory %v", err)
	}

	return files
}
