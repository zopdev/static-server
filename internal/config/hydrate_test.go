package config

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gofr.dev/pkg/gofr/config"
	"gofr.dev/pkg/gofr/datasource/file"
)

var (
	errDiskFailure = errors.New("disk failure")
	errDiskFull    = errors.New("disk full")
)

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
			wantErr:  ErrMissingVars,
		},
		{
			name:     "all vars missing",
			template: `{"a":"${X}","b":"${Y}"}`,
			vars:     map[string]string{},
			expected: `{"a":"","b":""}`,
			wantErr:  ErrMissingVars,
		},
		{
			name:    "invalid config path",
			vars:    map[string]string{"CONFIG_FILE_PATH": "/no/such/file"},
			wantErr: ErrReadConfig,
		},
		{
			name:     "read error",
			template: "read-error",
			vars:     map[string]string{},
			wantErr:  ErrReadConfig,
		},
		{
			name:     "write error",
			template: "write-error",
			vars:     map[string]string{},
			wantErr:  ErrWriteConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockFS := file.NewMockFileSystem(ctrl)

			gotOutput := setupMocks(t, ctrl, mockFS, tt.template, tt.vars)

			err := HydrateFile(mockFS, config.NewMockConfig(tt.vars))

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.expected, string(gotOutput()))
		})
	}
}

func setupMocks(
	t *testing.T, ctrl *gomock.Controller, mockFS *file.MockFileSystem,
	template string, vars map[string]string,
) func() []byte {
	t.Helper()

	var output []byte

	switch {
	case template != "":
		vars[filePathVar] = "/mock/config.json"
		mockFile := file.NewMockFile(ctrl)
		mockFS.EXPECT().Open(vars[filePathVar]).Return(mockFile, nil)

		setupReadWrite(mockFile, template, &output)

	case vars[filePathVar] != "":
		mockFS.EXPECT().Open(vars[filePathVar]).Return(nil, ErrMissingFile)
	}

	return func() []byte { return output }
}

func setupReadWrite(mockFile *file.MockFile, template string, output *[]byte) {
	if template == "read-error" {
		mockFile.EXPECT().Read(gomock.Any()).Return(0, errDiskFailure)
		return
	}

	templateBytes := []byte(template)

	mockFile.EXPECT().Read(gomock.Any()).DoAndReturn(func(p []byte) (int, error) {
		n := copy(p, templateBytes)
		return n, io.EOF
	})

	if template == "write-error" {
		mockFile.EXPECT().Write(gomock.Any()).Return(0, errDiskFull)
		return
	}

	mockFile.EXPECT().Write(gomock.Any()).DoAndReturn(func(p []byte) (int, error) {
		*output = append([]byte{}, p...)
		return len(p), nil
	})
}
