package main

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
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

type LoggingHash struct {
	hash.Hash
}

func (l *LoggingHash) Write(p []byte) (n int, err error) {
	checksum := md5.Sum(p)
	logger.Debug("add to hash", "md5", hex.EncodeToString(checksum[:]))
	return l.Hash.Write(p)
}

func newHashWithLog(h hash.Hash) *LoggingHash {
	return &LoggingHash{Hash: h}
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
	cmdRoot.Flags().String("hash", "sha1", "hash algorithm to use")
	cmdRoot.Flags().StringP("file", "f", "Dockerfile", "path to dockerfile")
	cmdRoot.Flags().Bool("debug", false, "print debug logs")
	return cmdRoot
}

func handlerRoot(cmd *cobra.Command, args []string) {
	viper.BindPFlags(cmd.Flags())

	if viper.GetBool("debug") {
		logger = slog.New(slog.NewTextHandler(
			os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			},
		))
	}

	buildArgs := viper.GetStringMapString("build-arg")

	logger.Debug("buildArgs:", mapToAttr(buildArgs)...)

	content := must(os.ReadFile(viper.GetString("file")))

	res := must(parser.Parse(bytes.NewBuffer(content)))

	workdir := os.DirFS(args[0])

	var h hash.Hash

	switch algo := viper.GetString("hash"); algo {
	case "sha1":
		h = sha1.New()
	case "md5":
		h = md5.New()
	case "sha256":
		h = sha256.New()
	default:
		panic(fmt.Sprintf("unknown hash algorithm %s", algo))
	}

	if viper.GetBool("debug") {
		h = newHashWithLog(h)
	}

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
			must0(pathSha(workdir, file, h))
		}
	}

	addMapToHash(h, buildArgs)

	addSliceToHash(h, viper.GetStringSlice("platform"))

	addMapToHash(h, viper.GetStringMapString("label"))

	fmt.Fprintf(cmd.OutOrStdout(), "%x", h.Sum(nil))
}

func addMapToHash(h hash.Hash, m map[string]string) {
	keys := maps.Keys(m)
	sort.Strings(keys)
	for _, key := range keys {
		must(io.WriteString(h, key))
		must(io.WriteString(h, m[key]))
	}
}

func addSliceToHash(h hash.Hash, s []string) {
	sort.Strings(s)
	for _, platform := range s {
		must(io.WriteString(h, platform))
	}
}

func pathSha(fsys fs.FS, path string, h hash.Hash) error {
	stat, err := fs.Stat(fsys, path)
	if err != nil {
		return err
	}

	if !stat.IsDir() {
		return fileSha(fsys, path, h)
	}

	return dirSha(fsys, path, h)
}

func dirSha(fsys fs.FS, path string, h hash.Hash) error {
	children, err := fs.ReadDir(fsys, path)
	if err != nil {
		return fmt.Errorf("fs.ReadDir: %w", err)
	}

	for _, child := range children {
		childPath := filepath.Join(path, child.Name())
		io.WriteString(h, childPath)

		err := pathSha(fsys, childPath, h)
		if err != nil {
			return fmt.Errorf(
				"calculating hash for %s: %w", childPath, err,
			)
		}
	}

	logger.Debug(
		"add path to checksum",
		"path", path,
	)
	return nil
}

func fileSha(fsys fs.FS, path string, h hash.Hash) error {
	f, err := fsys.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	logger.Debug(
		"add path to checksum",
		"path", path,
	)

	return nil
}

func pathsFromDockerfile(
	res *parser.Result,
	buildArgs map[string]string,
) []string {
	shlex := shell.NewLex(res.EscapeToken)

	var expandBuildArgs instructions.SingleWordExpander = func(
		key string,
	) (string, error) {
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
