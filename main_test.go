package main

import (
	"bytes"
	cryptoRand "crypto/rand"
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoc603/dockerfile-source-checksum/pkg/checksum"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/stretchr/testify/require"
)

func TestPathsFromDockerfile(t *testing.T) {
	tmpDir := generateRandomFile(
		"a/1", "a/2",
		"b",
		"c/1/1",
		"d/1",
	)
	defer os.RemoveAll(tmpDir)

	content := must(os.ReadFile("testdata/Dockerfile"))
	res := must(parser.Parse(bytes.NewBuffer(content)))

	paths := checksum.PathsFromDockerfile(res, map[string]string{
		"ARG1": "b",
	})

	require.Equal(t, []string{"./a/*", "./b", "./c", "./d", "./dist"}, paths)
}

func TestChecksum(t *testing.T) {
	tmpDir := generateRandomFile(
		"a/1", "a/2",
		"b",
		"c/1/1",
		"d/1",
	)
	defer os.RemoveAll(tmpDir)

	args := []string{
		"-f", "testdata/Dockerfile",
		"--debug",
		"--build-arg", "ARG1=b",
		"--platform", "linux/amd64,linux/arm64",
		"--label", "label1=value1",
		tmpDir,
	}

	output1 := bytes.NewBuffer(nil)
	run1 := newCmdRoot()
	run1.SetArgs(args)
	run1.SetOut(output1)
	run1.Execute()

	output2 := bytes.NewBuffer(nil)
	run2 := newCmdRoot()
	run2.SetArgs(args)
	run2.SetOut(output2)
	run2.Execute()

	require.NotEmpty(t, output1.String())
	require.Equal(t, output1.String(), output2.String())
	fmt.Println(output1.String(), output2.String())
}

func generateRandomFile(paths ...string) string {
	tmpDir := must(os.MkdirTemp(os.TempDir(), "dockerfile-source-checksum"))
	for _, path := range paths {
		path = filepath.Join(tmpDir, path)
		must0(os.MkdirAll(filepath.Dir(path), 0o755))

		content := make([]byte, rand.Intn(2048))
		must(cryptoRand.Read(content))

		f := must(os.OpenFile(
			path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644,
		))
		defer f.Close()

		must(base64.NewEncoder(base64.StdEncoding, f).Write(content))
	}
	return tmpDir
}
