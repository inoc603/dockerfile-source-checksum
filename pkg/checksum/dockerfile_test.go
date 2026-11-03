package checksum

import (
	"bytes"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestNormalizeSourcePath(t *testing.T) {
	files := fstest.MapFS{
		"frontend/src/example.txt": &fstest.MapFile{Data: []byte("source")},
		"frontend/package.json":    &fstest.MapFile{Data: []byte("{}")},
		"arr[0]/file.txt":          &fstest.MapFile{Data: []byte("literal bracket")},
	}
	for _, source := range []string{
		"frontend", "frontend/", "./frontend", "./frontend/",
		"/frontend/", "../frontend/", "././frontend//",
	} {
		t.Run(source, func(t *testing.T) {
			require.Equal(t, "frontend", normalizeSourcePath(source))
			matches, err := fs.Glob(files, normalizeSourcePath(source))
			require.NoError(t, err)
			require.Equal(t, []string{"frontend"}, matches)
		})
	}
	for _, source := range []string{".", "./", "/"} {
		require.Equal(t, ".", normalizeSourcePath(source))
	}
	for _, tc := range []struct {
		source  string
		matches []string
	}{
		{"./frontend/*/", []string{"frontend/package.json", "frontend/src"}},
		{"./frontend/s?c/", []string{"frontend/src"}},
		{"./frontend/[sp]*/", []string{"frontend/package.json", "frontend/src"}},
		{"./arr[[]0]/", []string{"arr[0]"}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			matches, err := fs.Glob(files, normalizeSourcePath(tc.source))
			require.NoError(t, err)
			require.Equal(t, tc.matches, matches)
		})
	}
}

func TestDirectorySourcesIncludeNestedChanges(t *testing.T) {
	for _, command := range []string{"COPY", "ADD"} {
		for _, source := range []string{
			"frontend", "frontend/", "./frontend", "./frontend/",
			"/frontend/", "../frontend/", "./frontend/*/", "./frontend/s?c/", "./frontend/[s]*/",
		} {
			t.Run(command+" "+source, func(t *testing.T) {
				config, _ := checksumFixture(t, "FROM scratch\n"+command+" "+source+" /frontend/\n")
				file := filepath.Join(config.Workdir, "frontend", "src", "example.txt")
				writeTestFile(t, file, "before")
				before := calculateChecksum(t, config)
				writeTestFile(t, file, "after")
				after := calculateChecksum(t, config)
				require.NotEqual(t, before, after)
				writeTestFile(t, filepath.Join(config.Workdir, "frontend", "src", "nested", "new.txt"), "new")
				require.NotEqual(t, after, calculateChecksum(t, config))
			})
		}
	}
}

func TestCopyFromExcludesLocalChanges(t *testing.T) {
	config, logs := checksumFixture(t, "FROM scratch AS builder\nFROM scratch\nCOPY --from=builder frontend/ /frontend/\n")
	file := filepath.Join(config.Workdir, "frontend", "src", "example.txt")
	writeTestFile(t, file, "before")
	before := calculateChecksum(t, config)
	writeTestFile(t, file, "after")
	require.Equal(t, before, calculateChecksum(t, config))
	require.Empty(t, logs.String())
}

func TestUnmatchedSourcesRemainSupported(t *testing.T) {
	for _, source := range []string{"generated/", "./generated/*/"} {
		t.Run(source, func(t *testing.T) {
			config, logs := checksumFixture(t, "FROM scratch\nCOPY "+source+" /app/\n")
			before := calculateChecksum(t, config)
			require.Equal(t, before, calculateChecksum(t, config))
			require.Contains(t, logs.String(), "checksum omits this source")
			require.Contains(t, logs.String(), "path="+source)
			writeTestFile(t, filepath.Join(config.Workdir, "generated", "file.txt"), "generated")
			logs.Reset()
			require.NotEqual(t, before, calculateChecksum(t, config))
			require.Empty(t, logs.String())
		})
	}
}

func TestMalformedSourcePatternReturnsError(t *testing.T) {
	config, _ := checksumFixture(t, "FROM scratch\nCOPY frontend[/ /app/\n")
	_, err := CalculateDockerfileChecksum(config)
	require.ErrorIs(t, err, path.ErrBadPattern)
	require.Contains(t, err.Error(), "glob source")
}

func checksumFixture(t *testing.T, dockerfile string) (Config, *bytes.Buffer) {
	t.Helper()
	workdir := t.TempDir()
	config := Config{
		Workdir: workdir, Dockerfile: filepath.Join(workdir, "Dockerfile"),
		Hash: "sha1", BuildArgs: map[string]string{},
	}
	writeTestFile(t, config.Dockerfile, dockerfile)
	logs := new(bytes.Buffer)
	config.SetLogger(slog.New(slog.NewTextHandler(logs, nil)))
	return config, logs
}

func calculateChecksum(t *testing.T, config Config) string {
	t.Helper()
	checksum, err := CalculateDockerfileChecksum(config)
	require.NoError(t, err)
	return checksum
}

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0755))
	require.NoError(t, os.WriteFile(name, []byte(content), 0644))
}
