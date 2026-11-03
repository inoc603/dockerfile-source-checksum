package checksum

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/stretchr/testify/require"
)

func TestVariableScopes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dockerfile string
		args       map[string]string
		want       []string
	}{
		{
			name:       "stage defaults and instruction order",
			dockerfile: "FROM scratch\nARG SRC=first\nCOPY $SRC /out/\nARG SRC=second\nADD $SRC /out/\nENV SRC=third\nRUN --mount=type=bind,source=$SRC,target=/in true\n",
			want:       []string{"first", "second", "third"},
		},
		{
			name:       "global args require stage declaration",
			dockerfile: "ARG SRC=global\nFROM scratch\nCOPY ${SRC:-fallback} /out/\nARG SRC\nCOPY $SRC /out/\n",
			want:       []string{"fallback", "global"},
		},
		{
			name:       "CLI args require stage declaration",
			dockerfile: "FROM scratch\nCOPY ${SRC:-fallback} /out/\nARG SRC=default\nCOPY $SRC /out/\n",
			args:       map[string]string{"SRC": "override"},
			want:       []string{"fallback", "override"},
		},
		{
			name:       "sequential global defaults and expanded stage defaults",
			dockerfile: "ARG ROOT=source\nARG SRC=${ROOT}/global\nFROM scratch\nARG SRC\nCOPY $SRC /out/\nARG OTHER=${SRC}/nested\nCOPY $OTHER /out/\n",
			args:       map[string]string{"ROOT": "override"},
			want:       []string{"override/global", "override/global/nested"},
		},
		{
			name:       "valueless global participates as empty",
			dockerfile: "ARG ROOT\nARG SRC=prefix${ROOT-fallback}\nFROM scratch\nARG SRC\nCOPY $SRC/file /out/\n",
			want:       []string{"prefix/file"},
		},
		{
			name:       "valueless global replaces earlier default",
			dockerfile: "ARG SRC=old\nARG SRC\nFROM scratch\nARG SRC\nCOPY ${SRC:-fallback}/file /out/\n",
			want:       []string{"fallback/file"},
		},
		{
			name:       "stage default beats global default",
			dockerfile: "ARG SRC=global\nFROM scratch\nARG SRC=local\nCOPY $SRC /out/\n",
			want:       []string{"local"},
		},
		{
			name:       "empty override and literal override",
			dockerfile: "FROM scratch\nARG SRC=default\nARG OTHER=default\nCOPY ${SRC-empty} ${SRC:-fallback} $OTHER /out/\n",
			args:       map[string]string{"SRC": "", "OTHER": "$SRC"},
			want:       []string{"", "$SRC", "fallback"},
		},
		{
			name:       "unrelated stages start empty",
			dockerfile: "FROM scratch AS first\nARG SRC=arg\nENV OTHER=env\nFROM scratch\nCOPY ${SRC:-fallback} ${OTHER:-other-fallback} /out/\n",
			want:       []string{"fallback", "other-fallback"},
		},
		{
			name:       "named parent inheritance is isolated from siblings",
			dockerfile: "ARG BASE=parent\nFROM scratch AS parent\nARG SRC=arg\nENV OTHER=env\nFROM parent AS sibling\nENV SRC=wrong OTHER=wrong\nFROM ${BASE}\nCOPY $SRC $OTHER /out/\n",
			want:       []string{"arg", "env"},
		},
		{
			name:       "instruction expansion uses previous environment",
			dockerfile: "FROM scratch\nENV SRC=old\nENV SRC=new OTHER=$SRC\nCOPY $SRC $OTHER /out/\n",
			want:       []string{"new", "old"},
		},
		{
			name:       "bare unresolved ARG preserves value",
			dockerfile: "FROM scratch\nENV SRC=env\nARG SRC\nCOPY $SRC /out/\n",
			want:       []string{"env"},
		},
		{
			// Match the pinned BuildKit converter: valued ARG updates the
			// effective environment, while derived stages reapply image ENV.
			name:       "valued ARG and inherited persistent ENV",
			dockerfile: "FROM scratch AS parent\nENV SRC=env\nARG SRC=arg\nCOPY $SRC /out/\nFROM parent\nCOPY $SRC /out/\n",
			want:       []string{"arg", "env"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := parser.Parse(strings.NewReader(tc.dockerfile))
			require.NoError(t, err)
			require.Equal(t, tc.want, PathsFromDockerfile(res, tc.args))
			// Parsing and expansion must not change either the caller's map
			// or the result of subsequent calls with the same AST.
			require.Equal(t, tc.want, PathsFromDockerfile(res, tc.args))
		})
	}
}

func TestBuildArgsRemainUnchanged(t *testing.T) {
	config, _ := checksumFixture(t, "ARG GLOBAL=default\nFROM scratch\nARG VERSION\nENV VERSION=fixed\n")
	config.BuildArgs = map[string]string{"VERSION": "one"}
	before := calculateChecksum(t, config)
	require.Equal(t, map[string]string{"VERSION": "one"}, config.BuildArgs)
	require.Equal(t, before, calculateChecksum(t, config))
	config.BuildArgs["VERSION"] = "two"
	require.NotEqual(t, before, calculateChecksum(t, config))
	require.Equal(t, map[string]string{"VERSION": "two"}, config.BuildArgs)
}

func TestScopedVariablesIncludeActualSourceChanges(t *testing.T) {
	for _, dockerfile := range []string{
		"FROM scratch\nARG SRC=actual\nCOPY ${SRC:-wrong} /out/\n",
		"FROM scratch AS unused\nENV SRC=wrong\nFROM scratch\nCOPY ${SRC:-actual} /out/\n",
	} {
		t.Run(dockerfile, func(t *testing.T) {
			config, _ := checksumFixture(t, dockerfile)
			actual := filepath.Join(config.Workdir, "actual")
			writeTestFile(t, actual, "before")
			writeTestFile(t, filepath.Join(config.Workdir, "wrong"), "before")
			before := calculateChecksum(t, config)
			writeTestFile(t, actual, "after")
			after := calculateChecksum(t, config)
			require.NotEqual(t, before, after)
			require.NoError(t, os.Remove(filepath.Join(config.Workdir, "wrong")))
			require.Equal(t, after, calculateChecksum(t, config))
		})
	}
}
