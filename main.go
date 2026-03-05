package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gofr.dev/pkg/gofr"
	"gofr.dev/pkg/gofr/config"
)

const defaultStaticFilePath = `./static`
const indexHTML = "/index.html"
const htmlExtension = ".html"
const rootPath = "/"

func hydrateConfig(cfg config.Config) error {
	configPath := cfg.Get("CONFIG_FILE_PATH")
	if configPath == "" {
		return nil
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Hydrate with available vars
	result := os.Expand(string(content), cfg.Get)

	if err := os.WriteFile(configPath, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	// Detect vars that were missing (replaced with empty string)
	re := regexp.MustCompile(`\$\{(\w+)\}`)
	matches := re.FindAllStringSubmatch(string(content), -1)
	var missing []string
	for _, m := range matches {
		if cfg.Get(m[1]) == "" {
			missing = append(missing, m[1])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing config variables: %v", missing)
	}

	return nil
}

func main() {
	app := gofr.New()

	if err := hydrateConfig(app.Config); err != nil {
		app.Logger().Error(err)
	}

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
