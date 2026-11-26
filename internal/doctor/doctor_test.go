package doctor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"

	"martianoff/gala/internal/build"
	"martianoff/gala/internal/depman/mod"
)

const (
	testVersion = "1.2.3"
	galaBin     = "/opt/gala/bin/gala"
	goBin       = "/usr/local/go/bin/go"
	goRoot      = "/usr/local/go"
	goCache     = "/home/u/.cache/go-build"
)

// healthyEnv is an environment every check passes in: gala and go on PATH,
// a current Go, matching SDKs, a small Go build cache, and caches under a
// throwaway gala home. Nothing reads the real home directory or runs go.
func healthyEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	return Env{
		Version:    testVersion,
		Executable: galaBin,
		LookPath: func(name string) (string, error) {
			switch name {
			case "gala":
				return galaBin, nil
			case "go":
				return goBin, nil
			}
			return "", errors.New("not found")
		},
		GoEnv: func(string, ...string) ([]string, error) {
			return []string{"go1.25.5", goRoot, goCache}, nil
		},
		GoSDKRoot:   func() string { return goRoot },
		SameFile:    func(a, b string) bool { return a == b },
		GalaVersion: func(string) (string, error) { return "0.9.0", nil },
		DirSize: func(dir string) (int64, error) {
			if dir == goCache {
				return 1 << 30, nil
			}
			return DirSize(dir)
		},
		Config: &build.Config{
			GalaHome:   home,
			StdlibDir:  filepath.Join(home, "stdlib"),
			GalaPkgDir: filepath.Join(home, "pkg", "mod"),
		},
		GoCacheWarnBytes: DefaultGoCacheWarnBytes,
	}
}

// statusOf returns the status of the named check, or "" when it did not run.
func statusOf(r Report, name string) Status {
	for _, c := range r.Checks {
		if c.Name == name {
			return c.Status
		}
	}
	return ""
}

func TestRun(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, env *Env)
		check  string
		want   Status
		failed bool
	}{
		{name: "healthy gala", check: "gala", want: OK},
		{name: "healthy go", check: "go", want: OK},
		{name: "healthy GOROOT", check: "GOROOT", want: OK},
		{name: "healthy go build cache", check: "go build cache", want: OK},
		{name: "healthy stdlib cache", check: "stdlib cache", want: OK},
		{name: "outside a project", check: "project", want: OK},
		{
			name: "gala not on PATH",
			mutate: func(_ *testing.T, env *Env) {
				lookPath := env.LookPath
				env.LookPath = func(name string) (string, error) {
					if name == "gala" {
						return "", errors.New("not found")
					}
					return lookPath(name)
				}
			},
			check: "gala", want: Warn,
		},
		{
			name: "an older gala first on PATH",
			mutate: func(_ *testing.T, env *Env) {
				env.Executable = "/home/u/bin/gala"
			},
			check: "gala", want: Warn,
		},
		{
			name: "own path unknown, an older gala on PATH",
			mutate: func(_ *testing.T, env *Env) {
				env.Executable = ""
			},
			check: "gala", want: Warn,
		},
		{
			name: "unreadable go build cache",
			mutate: func(_ *testing.T, env *Env) {
				env.DirSize = func(string) (int64, error) { return 0, os.ErrPermission }
			},
			check: "go build cache", want: Warn,
		},
		{
			name: "go build cache not created yet",
			mutate: func(_ *testing.T, env *Env) {
				env.DirSize = func(string) (int64, error) { return 0, os.ErrNotExist }
			},
			check: "go build cache", want: OK,
		},
		{
			name: "a gala on PATH whose version cannot be read",
			mutate: func(_ *testing.T, env *Env) {
				env.Executable = "/home/u/bin/gala"
				env.GalaVersion = func(string) (string, error) { return "", errors.New("exit status 1") }
			},
			check: "gala", want: Warn,
		},
		{
			name: "a shim on PATH that runs the same version",
			mutate: func(_ *testing.T, env *Env) {
				env.Executable = "/home/u/scoop/apps/gala/current/gala"
				env.GalaVersion = func(string) (string, error) { return testVersion, nil }
			},
			check: "gala", want: OK,
		},
		{
			name: "gala found in the current directory",
			mutate: func(_ *testing.T, env *Env) {
				env.LookPath = func(name string) (string, error) {
					if name == "gala" {
						return galaBin, exec.ErrDot
					}
					return goBin, nil
				}
			},
			check: "gala", want: OK,
		},
		{
			name: "go env prints too few values",
			mutate: func(_ *testing.T, env *Env) {
				env.GoEnv = func(string, ...string) ([]string, error) { return []string{"go1.25.5"}, nil }
			},
			check: "go", want: Fail, failed: true,
		},
		{
			name: "go env failing skips the build cache",
			mutate: func(_ *testing.T, env *Env) {
				env.GoEnv = func(string, ...string) ([]string, error) { return nil, errors.New("exit status 1") }
			},
			check: "go build cache", want: "", failed: true,
		},
		{
			name: "go not on PATH",
			mutate: func(_ *testing.T, env *Env) {
				env.LookPath = func(name string) (string, error) {
					if name == "go" {
						return "", errors.New("not found")
					}
					return galaBin, nil
				}
			},
			check: "go", want: Fail, failed: true,
		},
		{
			name: "go not on PATH skips the build cache",
			mutate: func(_ *testing.T, env *Env) {
				env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
			},
			check: "go build cache", want: "", failed: true,
		},
		{
			name: "go env fails",
			mutate: func(_ *testing.T, env *Env) {
				env.GoEnv = func(string, ...string) ([]string, error) { return nil, errors.New("exit status 1") }
			},
			check: "go", want: Fail, failed: true,
		},
		{
			name: "go older than the minimum",
			mutate: func(_ *testing.T, env *Env) {
				env.GoEnv = func(string, ...string) ([]string, error) {
					return []string{"go1.24.9", goRoot, goCache}, nil
				}
			},
			check: "go", want: Fail, failed: true,
		},
		{
			name: "go at exactly the minimum",
			mutate: func(_ *testing.T, env *Env) {
				env.GoEnv = func(string, ...string) ([]string, error) {
					return []string{"go" + build.MinGoVersion, goRoot, goCache}, nil
				}
			},
			check: "go", want: OK,
		},
		{
			name: "development go",
			mutate: func(_ *testing.T, env *Env) {
				env.GoEnv = func(string, ...string) ([]string, error) {
					return []string{"devel go1.27-abcdef", goRoot, goCache}, nil
				}
			},
			check: "go", want: Warn,
		},
		{
			name: "no Go SDK for type inference",
			mutate: func(_ *testing.T, env *Env) {
				env.GoSDKRoot = func() string { return "" }
			},
			check: "GOROOT", want: Fail, failed: true,
		},
		{
			name: "type inference reads another SDK than go builds with",
			mutate: func(_ *testing.T, env *Env) {
				env.GoSDKRoot = func() string { return "/opt/old-go" }
			},
			check: "GOROOT", want: Warn,
		},
		{
			// The go on PATH switches toolchain to build, and type inference
			// reads that one: no advice to point GOROOT back at the old SDK.
			name: "type inference reads the toolchain an old go switches to",
			mutate: func(_ *testing.T, env *Env) {
				env.GoEnv = func(string, ...string) ([]string, error) {
					return []string{"go1.23.4", goRoot, goCache}, nil
				}
				env.GoSDKRoot = func() string { return "/opt/go1.25.0" }
			},
			check: "GOROOT", want: OK, failed: true, // the go check fails
		},
		{
			name: "large go build cache",
			mutate: func(_ *testing.T, env *Env) {
				env.GoCacheWarnBytes = 1 << 20
			},
			check: "go build cache", want: Warn,
		},
		{
			name: "go build cache off",
			mutate: func(_ *testing.T, env *Env) {
				env.GoEnv = func(string, ...string) ([]string, error) {
					return []string{"go1.25.5", goRoot, "off"}, nil
				}
			},
			check: "go build cache", want: OK,
		},
		{
			name: "stdlib directories of other versions",
			mutate: func(t *testing.T, env *Env) {
				for _, v := range []string{"v0.9.0", "v1.0.0.staging-42"} {
					require.NoError(t, os.MkdirAll(filepath.Join(env.Config.StdlibDir, v), 0o755))
				}
				require.NoError(t, os.MkdirAll(env.Config.StdlibVersionDir(testVersion), 0o755))
			},
			check: "stdlib cache", want: Warn,
		},
		{
			name: "long paths off",
			mutate: func(_ *testing.T, env *Env) {
				env.LongPathsEnabled = func() (bool, error) { return false, nil }
			},
			check: "long paths", want: Warn,
		},
		{
			name: "long paths on",
			mutate: func(_ *testing.T, env *Env) {
				env.LongPathsEnabled = func() (bool, error) { return true, nil }
			},
			check: "long paths", want: OK,
		},
		{
			name:  "long paths not checked off Windows",
			check: "long paths", want: "",
		},
		{
			name:  "module cache inside the gala home is not listed",
			check: "module cache", want: "",
		},
		{
			name: "module cache moved by GALA_CACHE",
			mutate: func(t *testing.T, env *Env) {
				env.Config.GalaPkgDir = t.TempDir()
			},
			check: "module cache", want: OK,
		},
		{
			name: "gala.mod parses",
			mutate: func(t *testing.T, env *Env) {
				env.ProjectDir = project(t, "module example.com/app\n", "")
			},
			check: "gala.mod", want: OK,
		},
		{
			name: "gala.mod does not parse",
			mutate: func(t *testing.T, env *Env) {
				env.ProjectDir = project(t, "module example.com/app\nfrobnicate x\n", "")
			},
			check: "gala.mod", want: Fail, failed: true,
		},
		{
			name: "go.mod agrees with gala.mod",
			mutate: func(t *testing.T, env *Env) {
				env.ProjectDir = project(t,
					"module example.com/app\n\nrequire github.com/google/uuid v1.3.0 // go\n",
					"module example.com/app\n\ngo 1.25\n\nrequire github.com/google/uuid v1.3.0\n")
			},
			check: "go.mod", want: OK,
		},
		{
			name: "go.mod disagrees with gala.mod",
			mutate: func(t *testing.T, env *Env) {
				env.ProjectDir = project(t,
					"module example.com/app\n\nrequire github.com/google/uuid v1.3.0 // go\n",
					"module example.com/app\n\ngo 1.25\n\nrequire github.com/google/uuid v1.6.0\n")
			},
			check: "go.mod", want: Warn,
		},
		{
			name: "no go.mod is not drift",
			mutate: func(t *testing.T, env *Env) {
				env.ProjectDir = project(t, "module example.com/app\n", "")
			},
			check: "go.mod", want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := healthyEnv(t)
			if tt.mutate != nil {
				tt.mutate(t, &env)
			}
			r := Run(env)
			require.Equal(t, tt.want, statusOf(r, tt.check), "%+v", r.Checks)
			require.Equal(t, tt.failed, r.Failed(), "%+v", r.Checks)
			for _, c := range r.Checks {
				if c.Status == OK {
					require.Empty(t, c.Fix, c.Name)
					require.Empty(t, c.DocsURL, c.Name)
				} else {
					require.NotEmpty(t, c.Fix, c.Name)
					require.True(t, strings.HasPrefix(c.DocsURL, TroubleshootingURL+"#"), c.DocsURL)
				}
			}
		})
	}
}

// project writes a project with the given gala.mod and, unless goMod is
// empty, go.mod, and returns its directory.
func project(t *testing.T, galaMod, goMod string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gala.mod"), []byte(galaMod), 0o644))
	if goMod != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644))
	}
	return dir
}

func TestModDrift(t *testing.T) {
	tests := []struct {
		name    string
		galaMod string
		goMod   string
		want    []string
	}{
		{
			name:    "same versions",
			galaMod: "require a.com/x v1.0.0 // go",
			goMod:   "require a.com/x v1.0.0",
		},
		{
			name:    "different version",
			galaMod: "require a.com/x v1.0.0 // go",
			goMod:   "require a.com/x v1.1.0",
			want:    []string{"a.com/x (gala.mod v1.0.0, go.mod v1.1.0)"},
		},
		{
			name:    "direct go.mod requirement missing from gala.mod",
			galaMod: "",
			goMod:   "require a.com/x v1.0.0",
			want:    []string{"a.com/x v1.0.0 (go.mod only)"},
		},
		{
			name:    "indirect go.mod requirement missing from gala.mod",
			galaMod: "",
			goMod:   "require a.com/x v1.0.0 // indirect",
		},
		{
			name:    "indirect go.mod requirement at another version",
			galaMod: "require a.com/x v1.0.0 // indirect, go",
			goMod:   "require a.com/x v1.2.0 // indirect",
			want:    []string{"a.com/x (gala.mod v1.0.0, go.mod v1.2.0)"},
		},
		{
			name:    "a GALA dependency go.mod also requires",
			galaMod: "require a.com/galalib v1.0.0",
			goMod:   "require a.com/galalib v1.0.0",
		},
		{
			name:    "a replaced module, replaced in both",
			galaMod: "require a.com/x v0.0.0 // go\n\nreplace a.com/x => ../x",
			goMod:   "require a.com/x v0.0.0\n\nreplace a.com/x => ../x",
		},
		{
			name:    "a go.mod replace missing from gala.mod",
			galaMod: "require a.com/x v0.0.0 // go",
			goMod:   "require a.com/x v0.0.0\n\nreplace a.com/x => ../x",
			want:    []string{"replace a.com/x => ../x (go.mod only)"},
		},
		{
			name:    "replaced with a different version",
			galaMod: "require a.com/x v1.0.0 // go\n\nreplace a.com/x => a.com/x v1.1.0",
			goMod:   "require a.com/x v1.0.0\n\nreplace a.com/x => a.com/x v1.2.0",
			want:    []string{"replace a.com/x => a.com/x v1.1.0 (gala.mod only)", "replace a.com/x => a.com/x v1.2.0 (go.mod only)"},
		},
		{
			name:    "a gala.mod replace of a GALA dependency is not drift",
			galaMod: "require a.com/galalib v1.0.0\n\nreplace a.com/galalib => ../galalib",
			goMod:   "",
		},
		{
			name:    "a go.mod replace gala.mod cannot hold, replaced in gala.mod with another path",
			galaMod: "require a.com/x v0.0.0 // go\n\nreplace a.com/x => ../mydir",
			goMod:   "require a.com/x v0.0.0\n\nreplace a.com/x => \"../my dir\"",
		},
		{
			name:    "a go.mod replace gala.mod cannot hold, missing from gala.mod with its requirement",
			galaMod: "",
			goMod:   "require a.com/x v0.0.0\n\nreplace a.com/x => \"../my dir\"",
			want: []string{
				"a.com/x v0.0.0 (go.mod only)",
				"replace a.com/x v0.0.0 (go.mod only, with a path gala.mod cannot hold: give gala.mod a replace with another path)",
			},
		},
		{
			name:    "an unwritable exact-version replace is not stood in for by a replace copied from go.mod",
			galaMod: "require a.com/x v1.0.0 // go\n\nreplace a.com/x => ../ok",
			goMod:   "require a.com/x v1.0.0\n\nreplace a.com/x v1.0.0 => \"../a b\"\n\nreplace a.com/x => ../ok",
			want:    []string{"replace a.com/x v1.0.0 (go.mod only, with a path gala.mod cannot hold: give gala.mod a replace with another path)"},
		},
		{
			name:    "an unwritable replace limited to a version go.mod does not require is not drift",
			galaMod: "require a.com/x v1.2.0 // go",
			goMod:   "require a.com/x v1.2.0\n\nreplace a.com/x v1.0.0 => \"../a b\"",
		},
		{
			name:    "gala.mod standing in for an unwritable exact-version replace for every version is not drift",
			galaMod: "require a.com/x v1.0.0 // go\n\nreplace a.com/x => ../ab",
			goMod:   "require a.com/x v1.0.0\n\nreplace a.com/x v1.0.0 => \"../a b\"",
		},
		{
			name:    "gala.mod standing in for an unwritable replace for every version at the required one is not drift",
			galaMod: "require a.com/x v0.0.0 // go\n\nreplace a.com/x v0.0.0 => ../mydir",
			goMod:   "require a.com/x v0.0.0\n\nreplace a.com/x => \"../my dir\"",
		},
		{
			name:    "gala.mod standing in for an unwritable replace no requirement uses is not drift",
			galaMod: "require a.com/x v1.2.0 // go\n\nreplace a.com/x v1.0.0 => ../ab",
			goMod:   "require a.com/x v1.2.0\n\nreplace a.com/x v1.0.0 => \"../a b\"",
		},
		{
			name:    "a GALA package go.mod replaces with a path with a space is not drift",
			galaMod: "",
			goMod:   "require martianoff/gala/std v0.0.0\n\nreplace martianoff/gala/std => \"../gala std\"",
		},
		{
			name:    "a requirement a writable exact-version replace applies to, beside an unwritable one, is listed once",
			galaMod: "replace a.com/x v1.0.0 => ../ok",
			goMod:   "require a.com/x v1.0.0\n\nreplace a.com/x => \"../a b\"\n\nreplace a.com/x v1.0.0 => ../ok",
			want:    []string{"a.com/x v1.0.0 (go.mod only)"},
		},
		{
			name:    "gala.mod Go dependency missing from go.mod",
			galaMod: "require a.com/x v1.0.0 // go",
			goMod:   "",
			want:    []string{"a.com/x v1.0.0 (gala.mod only)"},
		},
		{
			name:    "GALA dependencies are not Go dependencies",
			galaMod: "require a.com/galalib v1.0.0",
			goMod:   "",
		},
		{
			name:    "the GALA standard library in go.mod is gala's own",
			galaMod: "",
			goMod:   "require martianoff/gala/std v0.0.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			galaMod, err := mod.Parse("module example.com/app\n\n" + tt.galaMod + "\n")
			require.NoError(t, err)
			goMod, err := modfile.ParseLax("go.mod", []byte("module example.com/app\n\n"+tt.goMod+"\n"), nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, modDrift(galaMod, goMod))
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1536, "1.5 KiB"},
		{20 << 30, "20.0 GiB"},
		{3 << 40, "3.0 TiB"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, formatBytes(tt.n))
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "one"), make([]byte, 10), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "b", "two"), make([]byte, 32), 0o644))

	size, err := DirSize(dir)
	require.NoError(t, err)
	require.Equal(t, int64(42), size)

	_, err = DirSize(filepath.Join(dir, "missing"))
	require.Error(t, err)
}
