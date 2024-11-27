package main

import (
	"gofr.dev/pkg/gofr"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	app := gofr.New()

	files := createListOfFiles()

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

func createListOfFiles() map[string]bool {
	files := make(map[string]bool)

	files["/"] = true

	filepath.Walk("./website", func(path string, info fs.FileInfo, err error) error {
		after, _ := strings.CutPrefix(path, "website")
		if !info.IsDir() {
			files[after] = true
		}

		return nil
	})

	return files
}
