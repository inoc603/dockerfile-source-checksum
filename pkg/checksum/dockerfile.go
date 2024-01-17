package checksum

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
	"sort"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"
)

type Config struct {
	BuildArgs  map[string]string `mapstructure:"build-arg"`
	Labels     map[string]string `mapstructure:"label"`
	Platforms  []string          `mapstructure:"platform"`
	Dockerfile string            `mapstructure:"file"`
	Workdir    string            `mapstructure:"workdir"`
	Hash       string            `mapstructure:"hash"`
	Debug      bool              `mapstructure:"debug"`

	logger *slog.Logger
}

func (c *Config) SetLogger(l *slog.Logger) {
	c.logger = l
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

// CalculateDockerfileChecksum returns a source-based checksum for a dockerfile.
func CalculateDockerfileChecksum(c Config) (string, error) {
	c.logger.Debug("buildArgs:", mapToAttr(c.BuildArgs)...)

	content, err := os.ReadFile(c.Dockerfile)
	if err != nil {
		return "", errors.Wrap(err, "read dockerfile")
	}

	res, err := parser.Parse(bytes.NewBuffer(content))
	if err != nil {
		return "", errors.Wrap(err, "parse dockerfile")
	}

	workdir := os.DirFS(c.Workdir)

	var h hash.Hash

	switch c.Hash {
	case "sha1":
		h = sha1.New()
	case "md5":
		h = md5.New()
	case "sha256":
		h = sha256.New()
	default:
		panic(fmt.Sprintf("unknown hash algorithm %s", c.Hash))
	}

	if c.Debug {
		h = newHashWithLog(h, c.logger)
	}

	// Add dockerfile to checksum
	c.logger.Debug(
		"add dockerfile to checksum",
		"workdir", workdir,
		"dockerfile", c.Dockerfile,
	)
	must(h.Write(content))

	// Add copied source to checksum
	paths := PathsFromDockerfile(res, c.BuildArgs)
	for _, path := range paths {
		c.logger.Debug("calculate checksum for path", "path", path)
		if strings.HasPrefix(path, "./") {
			path = must(filepath.Rel(".", path))
		}

		for _, file := range must(fs.Glob(workdir, path)) {
			must(io.WriteString(h, file))
			must0(pathSha(workdir, file, h))
		}
	}

	addMapToHash(h, c.BuildArgs)

	addSliceToHash(h, c.Platforms)

	addMapToHash(h, c.Labels)

	return fmt.Sprintf("%x", h.Sum(nil)), nil
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

	return nil
}

// PathsFromDockerfile returns paths added to a dockerfile.
func PathsFromDockerfile(
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

type LoggingHash struct {
	hash.Hash
	logger *slog.Logger
}

func (l *LoggingHash) Write(p []byte) (n int, err error) {
	checksum := md5.Sum(p)
	l.logger.Debug("add to hash", "md5", hex.EncodeToString(checksum[:]))
	return l.Hash.Write(p)
}

func newHashWithLog(h hash.Hash, l *slog.Logger) *LoggingHash {
	return &LoggingHash{Hash: h, logger: l}
}

func must[T any](v T, err error) T {
	must0(err)
	return v
}

func must0(err error) {
	if err != nil {
		panic(err)
	}
}
