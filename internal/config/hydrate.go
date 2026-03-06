package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"gofr.dev/pkg/gofr/config"
	"gofr.dev/pkg/gofr/datasource/file"
)

var (
	ErrMissingVars = errors.New("missing config variables")
	ErrReadConfig  = errors.New("failed to read config file")
	ErrWriteConfig = errors.New("failed to write config file")
	ErrMissingFile = errors.New("file not found")

	envVarRe = regexp.MustCompile(`\$\{(\w+)\}`)
)

const filePathVar = "CONFIG_FILE_PATH"

func HydrateFile(fs file.FileSystem, cfg config.Config) error {
	configPath := cfg.Get(filePathVar)
	if configPath == "" {
		return nil
	}

	configFile, err := fs.Open(filepath.Clean(configPath))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReadConfig, err)
	}

	content, err := io.ReadAll(configFile)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReadConfig, err)
	}

	_ = configFile.Close()

	// Hydrate with available vars
	result := os.Expand(string(content), cfg.Get)

	wf, err := fs.OpenFile(configPath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWriteConfig, err)
	}

	if _, err = wf.Write([]byte(result)); err != nil {
		return fmt.Errorf("%w: %w", ErrWriteConfig, err)
	}

	// Detect vars that were missing (replaced with empty string)
	matches := envVarRe.FindAllStringSubmatch(string(content), -1)

	var missing []string

	for _, m := range matches {
		if cfg.Get(m[1]) == "" {
			missing = append(missing, m[1])
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: %v", ErrMissingVars, missing)
	}

	return nil
}
