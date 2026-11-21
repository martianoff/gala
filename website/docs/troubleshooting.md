---
layout: default
title: "Troubleshooting GALA — gala doctor, Go SDK, Caches, and Windows"
description: "Fix environment problems with GALA: run gala doctor, install a Go SDK for type inference, clear stale stdlib caches and a growing Go build cache, keep gala.mod and go.mod in step, set up Windows, and report a bug with gala doctor --json."
keywords: "gala doctor, gala troubleshooting, gala go sdk not found, gala package not found, gala stdlib cache, go build cache size, gala.mod go.mod, gala windows long paths, gala bazel windows bash"
permalink: /docs/troubleshooting/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Troubleshooting</p>

{% raw %}

# Troubleshooting

Most problems with `gala` come from the environment it runs in rather than from your code: a missing or old Go, two installs of `gala`, a cache left behind by an older version, a `go.mod` that drifted from `gala.mod`. `gala doctor` checks for each of these and prints one line per check:

```sh
gala doctor
```

```
OK    gala            gala 0.103.0 at /home/you/.local/bin/gala
OK    go              go1.25.5 at /usr/local/go/bin/go (go1.25 or newer required)
OK    GOROOT          /usr/local/go
OK    gala home       /home/you/.gala, 2.1 GiB
WARN  stdlib cache    /home/you/.gala/stdlib/v0.103.0 (up to date); 6 directories from other gala versions use 48.3 MiB
                      fix: gala clean --stale-stdlib
                      see: https://gala.fyi/docs/troubleshooting/#stale-stdlib-cache
OK    go build cache  /home/you/.cache/go-build, 3.4 GiB
OK    gala.mod        /home/you/src/app/gala.mod (module example.com/app)
```

Every `WARN` and `FAIL` line is followed by a one-line fix and a link to the section of this page that explains it. `gala doctor` exits non-zero when a check fails, so it also works as a CI step. Give it a directory to check a project other than the one you are in (`gala doctor ./app`), and `--json` for output a program can read.

| Check | What it looks at |
|-------|------------------|
| `gala` | the running binary's version and path, and whether `gala` on `PATH` runs the same version |
| `go` | `go` on `PATH`, and that it is Go 1.25 or newer |
| `GOROOT` | the Go SDK that Go type inference reads, found the way a build finds it |
| `gala home` | where gala keeps its data (`GALA_HOME`, default `~/.gala`) and its size |
| `module cache` | the GALA module cache, when `GALA_CACHE` moves it out of the gala home |
| `stdlib cache` | stdlib directories that older gala versions extracted |
| `go build cache` | the size of the Go build cache (`go env GOCACHE`); it warns above 20 GiB |
| `long paths` | on Windows, whether long path support is on |
| `gala.mod` | in a project: that `gala.mod` parses |
| `go.mod` | in a project with a `go.mod`: that it requires the same Go dependencies as `gala.mod` |

---

## gala on PATH

`gala doctor` warns when no `gala` is on `PATH`, or when the `gala` on `PATH` is another binary that reports a different version. A package manager's shim that runs the same version is fine. Two installs, say one from Homebrew or Scoop and one from the install script, mean that whichever comes first on `PATH` runs, and an upgrade of the other changes nothing. The usual symptom is an error the release notes say was fixed.

List every `gala` on `PATH` with `which -a gala` (Linux, macOS) or `where.exe gala` (Windows), remove the ones you don't want, and run `gala version` to confirm.

An editor keeps the `gala lsp` it started, so after an upgrade restart the editor, or its language server, as well.

## Missing Go SDK

GALA transpiles to Go and builds with `go`, so `gala build`, `gala run` and `gala test` need Go 1.25 or newer on `PATH`. Install it from [go.dev/dl](https://go.dev/dl/).

The transpiler also reads Go packages to learn their types: that is how a Go call returning `(T, error)` becomes a `Try[T]`, and how a field or method of a Go type gets its type. It finds the Go SDK in this order:

1. inside a Bazel action, the Go SDK `rules_go` downloaded;
2. `GOROOT`, if it names a Go SDK;
3. the Go SDK the `gala` binary was built with, if it is on this machine;
4. a Go SDK above the `gala` binary's directory;
5. the SDK of `go` on `PATH`.

When it finds none, gala prints `Warning: Go SDK not found — Go type inference disabled` and carries on. Every Go type is then unknown, and the build fails later, on whatever needed one, with an error that does not mention the SDK. `gala doctor` reports this as a `FAIL` on the `GOROOT` line.

It warns, too, when the SDK type inference reads is not the one `go` on `PATH` builds with, usually because `GOROOT` points at an older install. GALA then infers types from one Go version and compiles with another. Set `GOROOT` to the output of `go env GOROOT`, or unset it if it points at an old install.

The go.mod files GALA generates ask for Go 1.25, so a `go` from 1.21 to 1.24 builds GALA code with a newer toolchain it downloads and switches to, under the default `GOTOOLCHAIN=auto`. With `GOTOOLCHAIN=local` the build fails instead, and a `go` older than 1.21 builds with itself, so code that needs a newer Go fails to compile. A `GOTOOLCHAIN` that names a toolchain switches too. When the SDK came from steps 3–5, type inference follows the switch: it asks the go command which toolchain it uses and reads that one. If the go command cannot tell, for example offline, gala prints a warning and reads the installed SDK.

`gala doctor` asks `go` for its version outside any module, so it reports the Go that is installed (or the toolchain `GOTOOLCHAIN` names), and fails when that is older than Go 1.25. Install Go 1.25 or newer rather than relying on the switch: it downloads a toolchain, and every gala process asks the go command again.

Under Bazel, the `.bazelrc` lines `build --action_env=GOROOT` and `build --action_env=PATH` pass the Go SDK through to the transpile actions; see [Required .bazelrc settings](/docs/dependency-management/#8-bazel-integration).

## Stale stdlib cache

`gala build` does not read the standard library from the binary directly. It extracts it once to `<gala home>/stdlib/v<version>/` and reads it from there. The directory is named after the gala version, and a marker file in it records which build of the standard library it holds, so a copy that does not match the running binary is extracted again on the next build.

Each version gets its own directory and nothing removes the old ones, so every upgrade leaves one behind. They are only disk space: no build reads them. `gala doctor` counts them, and this removes them:

```sh
gala clean --stale-stdlib
```

Close editors and stop builds that run another gala version first: a running `gala lsp` or build reads its version's directory and fails if it disappears. A still-installed older gala that runs again later extracts its directory again. Run this from the gala you use: a development build (version `dev`) counts every release's directory as stale.

### "package not found" after an upgrade

If a build reports `package not found` for a standard library package that the new version added:

1. Run `gala doctor`. If the `gala` line warns, an older `gala` comes first on `PATH`; see [gala on PATH](#gala-on-path).
2. Restart your editor, which keeps running the `gala lsp` of the old version.
3. Clear the project's analysis cache with `gala clean --cache`.
4. To rule the caches out entirely, build once with an empty gala home: `GALA_HOME=$(mktemp -d) gala build`. If that works and the normal build does not, [report a bug](#reporting-a-bug) with the output of both.

## Go build cache growth

`go build` caches every package it compiles in the Go build cache (`go env GOCACHE`; `~/.cache/go-build` on Linux, `~/Library/Caches/go-build` on macOS, `%LocalAppData%\go-build` on Windows). `gala build` and `gala test` compile through `go`, so they fill it like any Go build. Go removes only entries that went unused for several days and has no size limit, so frequent builds of several projects, or of many branches of one, can take it to tens or hundreds of gigabytes.

`gala doctor` warns when it passes 20 GiB. Clearing it is safe; the next build of each project is slower while it refills:

```sh
go clean -cache
```

The GALA side of a build lives in the gala home: the build workspaces under `build/` (`gala clean --all` removes them), the module caches under `pkg/mod` and `go/pkg/mod`, and the stdlib cache above. For Bazel, see [Build Disk Usage](https://github.com/martianoff/gala/blob/master/docs/BUILD_DISK_USAGE.MD).

## gala.mod does not parse

`gala doctor` fails when the project's `gala.mod` has a line it cannot read, and names the line (`gala.mod:4: unknown directive: requires`). Every `gala` command that reads the file stops on it the same way. [gala.mod](/docs/dependency-management/#galamod) lists the directives and their form.

## gala.mod and go.mod drift

A project with both files has two lists of Go dependencies: `gala build` takes Go module versions from `gala.mod`, and `go build`, `go test` and your editor's Go tooling take them from `go.mod`. `gala mod init` copies the requirements of `go.mod` into `gala.mod`, but only once. A later `go get` changes `go.mod` alone, and the two builds then use different versions of a dependency.

`gala doctor` compares the two and warns about:

- a module the two require at different versions;
- a Go dependency in `gala.mod` that `go.mod` does not require;
- a direct requirement of `go.mod` that `gala.mod` does not have. An indirect one (`// indirect`) missing from `gala.mod` is not counted: `go mod tidy` adds one for every module the build reaches.

The requirements `gala mod init` leaves out are left out here too: a module `go.mod` replaces, and the packages of GALA itself.

To fix one, record the version `go.mod` requires in `gala.mod`:

```sh
gala mod add github.com/google/uuid@v1.6.0 --go
```

In a Bazel project, `gala mod tidy` writes the Go dependencies of `gala.mod` into `go.mod` instead. See [Go Dependencies](/docs/dependency-management/#6-go-dependencies).

## Windows notes

### Long paths

Windows limits a path to 260 characters unless long path support is on. Build workspaces nest module paths several levels deep under the gala home, and Bazel output trees go deeper still. `gala` and `go` cope with long paths on their own, but tools that do not, such as Bazel and `git`, can fail on them. `gala doctor` warns when the setting is off. To turn it on, run this in an administrator PowerShell and restart:

```powershell
New-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Control\FileSystem" -Name LongPathsEnabled -Value 1 -PropertyType DWORD -Force
```

### PATH

The install script adds its directory to your user `PATH`. A terminal that was open before the install still has the old `PATH`; open a new one. `where.exe gala` and `where.exe go` list what `PATH` resolves to, in order.

### Bash for Bazel

Bazel on Windows runs `genrule` and shell actions through Bash. Install [Git for Windows](https://git-scm.com/download/win) or [MSYS2](https://www.msys2.org/) and put its `bash.exe` on the system `PATH`, or set the `BAZEL_SH` environment variable to it for your user. A build that fails with `CreateProcessW(...bash.exe ...)` cannot find it. Use [Bazelisk](https://github.com/bazelbuild/bazelisk) rather than a Bazel installed directly, so the version a project pins in `.bazelversion` is the one that runs.

Enable Developer Mode (Settings, System, For developers) so Bazel can create symlinks instead of copying runfiles; see [Build Disk Usage](https://github.com/martianoff/gala/blob/master/docs/BUILD_DISK_USAGE.MD#windows-enable-developer-mode).

## Reporting a bug

Open an issue at [github.com/martianoff/gala/issues](https://github.com/martianoff/gala/issues) and include:

- the output of `gala doctor --json`, run in the project that fails;
- the command you ran and its full output (`gala build --json` gives the diagnostics as JSON);
- the smallest `.gala` file that shows the problem, if it is a compiler error.

The `--json` report has one entry per check, with its `name`, `status` (`OK`, `WARN` or `FAIL`), `message`, and for a problem its `fix` and `docsUrl`. It contains paths on your machine, such as your home directory; edit them out if you need to.

{% endraw %}
