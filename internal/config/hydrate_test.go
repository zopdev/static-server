package config

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"gofr.dev/pkg/gofr/config"
	"gofr.dev/pkg/gofr/datasource/file"
	"gofr.dev/pkg/gofr/logging"
)

func writeTempFile(t *testing.T, fs file.FileSystem, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	f, err := fs.Create(path)
	require.NoError(t, err)

	_, err = f.Write([]byte(content))
	require.NoError(t, err)

	require.NoError(t, f.Close())

	return path
}

func TestConfig(t *testing.T) {
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		expected string
		wantErr  error
	}{
		{
			name:     "all vars present",
			template: `{"a":"${A}","b":"${B}"}`,
			vars:     map[string]string{"A": "1", "B": "2"},
			expected: `{"a":"1","b":"2"}`,
		},
		{
			name:    "no config path is a no-op",
			vars:    map[string]string{},
			wantErr: nil,
		},
		{
			name:     "extra vars ignored",
			template: `{"a":"${A}"}`,
			vars:     map[string]string{"A": "1", "EXTRA": "x"},
			expected: `{"a":"1"}`,
		},
		{
			name:     "partial vars missing",
			template: `{"a":"${A}","b":"${MISSING}"}`,
			vars:     map[string]string{"A": "1"},
			expected: `{"a":"1","b":""}`,
			wantErr:  errMissingVars,
		},
		{
			name:     "all vars missing",
			template: `{"a":"${X}","b":"${Y}"}`,
			vars:     map[string]string{},
			expected: `{"a":"","b":""}`,
			wantErr:  errMissingVars,
		},
		{
			name:    "invalid config path",
			vars:    map[string]string{"CONFIG_FILE_PATH": "/no/such/file"},
			wantErr: errReadConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := file.New(logging.NewMockLogger(logging.ERROR))

			if tt.template != "" {
				path := writeTempFile(t, fs, tt.template)
				tt.vars[filePathVar] = path
			}

			err := HydrateFile(fs, config.NewMockConfig(tt.vars))

			require.ErrorIs(t, err, tt.wantErr)

			if tt.expected != "" {
				rf, readErr := fs.Open(tt.vars[filePathVar])
				require.NoError(t, readErr)
				got, readErr := io.ReadAll(rf)
				require.NoError(t, readErr)
				require.Equal(t, tt.expected, string(got))
			}
		})
	}
}

func TestHydrateFile_WriteError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not effective on Windows")
	}

	fs := file.New(logging.NewMockLogger(logging.ERROR))
	path := writeTempFile(t, fs, `{"a":"${A}"}`)

	err := os.Chmod(path, 0444)
	require.NoError(t, err)
	vars := map[string]string{
		filePathVar: path,
		"A":         "1",
	}

	err = HydrateFile(fs, config.NewMockConfig(vars))
	require.ErrorIs(t, err, errWriteConfig)
}
