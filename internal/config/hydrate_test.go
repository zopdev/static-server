package config

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gofr.dev/pkg/gofr/config"
	"gofr.dev/pkg/gofr/datasource/file"
	"gofr.dev/pkg/gofr/logging"
)

func writeTempFile(t *testing.T, content string, permissions os.FileMode) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	if permissions != 0 {
		require.NoError(t, os.Chmod(path, permissions))
	}

	return path
}

func TestConfig(t *testing.T) {
	tests := []struct {
		name        string
		template    string
		vars        map[string]string
		permissions os.FileMode
		expected    string
		wantErr     error
	}{
		{
			name:     "all vars present",
			template: `{"a":"${A}","b":"${B}"}`,
			vars:     map[string]string{"A": "1", "B": "2"},
			expected: `{"a":"1","b":"2"}`,
		},
		{
			name:     "no config path is a no-op",
			template: `{"a":"${A}","b":"${B}"}`,
			vars:     map[string]string{"CONFIG_FILE_PATH": ""},
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
			name:     "invalid config path",
			template: `{"a":"${A}"}`,
			vars:     map[string]string{"CONFIG_FILE_PATH": "/no/such/file"},
			wantErr:  errReadConfig,
		},
		{
			name:        "write error on read-only file",
			template:    `{"a":"${A}"}`,
			vars:        map[string]string{"A": "1"},
			permissions: 0444,
			wantErr:     errWriteConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := file.New(logging.NewMockLogger(logging.ERROR))

			// To not overwrite the file path if already present in the test case
			if _, ok := tt.vars[filePathVar]; !ok {
				tt.vars[filePathVar] = writeTempFile(t, tt.template, tt.permissions)
			}

			err := HydrateFile(fs, config.NewMockConfig(tt.vars))

			require.ErrorIs(t, err, tt.wantErr)

			if tt.vars[filePathVar] == "" || tt.wantErr != nil {
				return
			}

			rf, readErr := os.Open(tt.vars[filePathVar])
			require.NoError(t, readErr)
			got, readErr := io.ReadAll(rf)
			require.NoError(t, readErr)
			require.Equal(t, tt.expected, string(got))
		})
	}
}
