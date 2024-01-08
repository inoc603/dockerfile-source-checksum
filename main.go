package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/exp/maps"
)

var logger = slog.New(slog.NewTextHandler(
	os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo},
))

func main() {
	newCmdRoot().Execute()
}

func newCmdRoot() *cobra.Command {
	cmdRoot := &cobra.Command{
		Use: "docker-source-checksum",
		Run: handlerRoot,
	}
	cmdRoot.Flags().StringToString(
		"build-arg",
		nil,
		"--build-arg for the docker build command",
	)
	cmdRoot.Flags().StringSlice(
		"platform",
		[]string{fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)},
		"--platform for the docker build command",
	)
	cmdRoot.Flags().StringToString(
		"label",
		nil,
		"--label for the docker build command",
	)
	cmdRoot.Flags().StringP("file", "f", "Dockerfile", "path to dockerfile")
	cmdRoot.Flags().Bool("debug", false, "print debug logs")
	return cmdRoot
}

func handlerRoot(cmd *cobra.Command, args []string) {
	viper.BindPFlags(cmd.Flags())

	if viper.GetBool("debug") {
		logger = slog.New(slog.NewTextHandler(
			os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug},
		))
	}

	buildArgs := viper.GetStringMapString("build-arg")

	logger.Debug("buildArgs:", mapToAttr(buildArgs)...)

	content := must(os.ReadFile(viper.GetString("file")))

	res := must(parser.Parse(bytes.NewBuffer(content)))

	workdir := os.DirFS(args[0])

	h := sha1.New()

	// Add dockerfile to checksum
	logger.Debug(
		"add dockerfile to checksum",
		"workdir", workdir,
		"dockerfile", viper.GetString("file"),
	)
	must(h.Write(content))

	// Add copied source to checksum
	paths := pathsFromDockerfile(res, buildArgs)
	for _, path := range paths {
		logger.Debug("calculate checksum for path", "path", path)
		if strings.HasPrefix(path, "./") {
			path = must(filepath.Rel(".", path))
		}

		for _, file := range must(fs.Glob(workdir, path)) {
			must(io.WriteString(h, file))
			must(h.Write(must(pathSha(workdir, file))))
		}
	}

	// Add build args to checksum
	buildArgsKeys := maps.Keys(buildArgs)
	sort.Strings(buildArgsKeys)
	for _, key := range buildArgsKeys {
		logger.Debug("add build arg to checksum", key, buildArgs[key])
		must(io.WriteString(h, key))
		must(io.WriteString(h, buildArgs[key]))
	}

	// Add platforms to checksum
	platforms := viper.GetStringSlice("platform")
	sort.Strings(platforms)
	for _, platform := range platforms {
		logger.Debug("add platform to checksum", "platform", platform)
		must(io.WriteString(h, platform))
	}

	labels := viper.GetStringMapString("label")
	labelKeys := maps.Keys(labels)
	sort.Strings(labelKeys)
	for _, key := range labelKeys {
		logger.Debug("add label to checksum", key, labels[key])
		must(io.WriteString(h, key))
		must(io.WriteString(h, labels[key]))
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%x", h.Sum(nil))
}

func pathSha(fsys fs.FS, path string) ([]byte, error) {
	stat, err := fs.Stat(fsys, path)
	if err != nil {
		return nil, err
	}

	if !stat.IsDir() {
		return fileSha(fsys, path)
	}

	return dirSha(fsys, path)
}

func dirSha(fsys fs.FS, path string) ([]byte, error) {
	h := sha1.New()

	children, err := fs.ReadDir(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("fs.ReadDir: %w", err)
	}

	for _, child := range children {
		childPath := filepath.Join(path, child.Name())
		io.WriteString(h, childPath)

		childHash, err := pathSha(fsys, childPath)
		if err != nil {
			return nil, fmt.Errorf(
				"calculating hash for %s: %w", childPath, err,
			)
		}

		h.Write(childHash)
	}

	checksum := h.Sum(nil)
	logger.Debug(
		"add path to checksum",
		"path", path,
		"checksum", hex.EncodeToString(checksum),
	)
	return checksum, nil
}

func fileSha(fsys fs.FS, path string) ([]byte, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}

	checksum := h.Sum(nil)
	logger.Debug(
		"add path to checksum",
		"path", path,
		"checksum", hex.EncodeToString(checksum),
	)
	return checksum, nil
}

func pathsFromDockerfile(res *parser.Result, buildArgs map[string]string) []string {
	shlex := shell.NewLex(res.EscapeToken)

	var expandBuildArgs instructions.SingleWordExpander = func(key string) (string, error) {
		return shlex.ProcessWordWithMap(key, buildArgs)
	}

	stages, argCommands, err := instructions.Parse(res.AST)
	must0(err)

	for _, argCmd := range argCommands {
		for _, arg := range argCmd.Args {
			if _, ok := buildArgs[arg.Key]; !ok && arg.Value != nil {
				buildArgs[arg.Key] = arg.ValueString()
			}
		}
	}

	var paths []string

	for _, stage := range stages {
		for _, iCmd := range stage.Commands {
			if expandable, ok := iCmd.(instructions.SupportsSingleWordExpansion); ok {
				must0(expandable.Expand(expandBuildArgs))
			}

			switch cmd := iCmd.(type) {
			case *instructions.CopyCommand:
				if cmd.From == "" {
					paths = append(paths, cmd.SourcePaths...)
				}
			case *instructions.AddCommand:
				paths = append(paths, cmd.SourcePaths...)
			case *instructions.EnvCommand:
				for _, env := range cmd.Env {
					buildArgs[env.Key] = env.Value
				}
			case *instructions.RunCommand:
				for _, mount := range instructions.GetMounts(cmd) {
					if mount.From == "" &&
						mount.Type == instructions.MountTypeBind {
						paths = append(paths, mount.Source)
					}
				}
			}
		}
	}

	sort.Strings(paths)

	return paths
}

func must0(err error) {
	if err != nil {
		panic(err)
	}
}

func must[T any](v T, err error) T {
	must0(err)
	return v
}

func mapToAttr(m map[string]string) []any {
	res := make([]any, 0, len(m))
	keys := maps.Keys(m)
	sort.Strings(keys)
	for _, key := range keys {
		res = append(res, slog.String(key, m[key]))
	}
	return res
}
