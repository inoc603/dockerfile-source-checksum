package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"github.com/inoc603/dockerfile-source-checksum/pkg/checksum"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

	var config checksum.Config
	viper.SetConfigType("yaml")
	viper.Unmarshal(&config)
	config.Workdir = args[0]
	config.SetLogger(logger)

	fmt.Fprint(
		cmd.OutOrStdout(),
		must(checksum.CalculateDockerfileChecksum(config)),
	)
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
