# Performance debugging

Optional knobs for diagnosing slow or memory-hungry transpiler runs. All are
off unless the env var is set, so production binaries carry no overhead.

## Stack traces map to GALA source

A runtime panic or stack trace from a GALA program reports **GALA source
positions** (`foo.gala:12`), not positions in the generated Go. For example, a
divide-by-zero panics with:

```
panic: runtime error: integer divide by zero
main.divide(...)
	foo.gala:4
main.main()
	foo.gala:8
```

This works through Go's own `//line` directive mechanism: the transpiler stamps
each statement and top-level declaration with its originating GALA line and
emits a `//line <file>.gala:<n>` directive before the generated Go, which the
Go compiler and runtime honor in panics and stack traces. There is no separate
source-map file and no runtime cost — the directives only affect the position
metadata the compiler already records.

**Accuracy.** The mapping is exact for ordinary statements (one GALA statement →
one Go statement). It is *approximate* — right file, line possibly off by a few
within the construct — for:

- statements that lower to several Go statements (e.g. `use x = acquire`, which
  becomes an assignment plus a deferred `Close()`), where only the first Go line
  sits under the directive; and
- IIFE-lowered constructs (`match`, `if`-expressions, `bind`/`also`
  do-notation), whose generated blocks are relocated relative to the source.

**Suppression.** Directives are only emitted when the transpiler knows the
source file path. Anonymous snippet transpilation (e.g. the LSP) emits none.

**Reserved names.** `__gala_line_*` is a reserved identifier prefix (used for the
internal markers that become directives); declaring a GALA identifier with that
prefix is a compile error.

**Dropping into the generated Go.** A debugger's line numbers derive from the
same compiler position info, so they follow the `//line` directives too. When
you do need to read the generated Go directly, it is kept legible (see
`docs/DESIGN.MD`, goal 5) — though it carries GALA's machinery and is not the
idiomatic Go a person would hand-write.

## When the transpiler eats RAM

`gala build`, `gala transpile`, and the bazel `gala_transpile` genrule all
invoke the same in-process pipeline. If a single invocation grows past a
few hundred MB, capture a heap profile rather than guessing.

### `GALA_HEAP_DUMP_DIR=<dir>`

Each time `runtime.MemStats.HeapAlloc` crosses a new 500 MB band, the
transpiler writes a heap profile to `<dir>/heap_<N>MB.pb`. Analyze with:

```bash
go tool pprof -top -nodecount 20 path/to/gala.exe heap_2000MB.pb
go tool pprof -tree -focus=<funcName> path/to/gala.exe heap_2000MB.pb
go tool pprof -inuse_objects -top path/to/gala.exe heap_2000MB.pb
```

`runtime.GC()` runs before each write, so live-only allocations are
captured. The `inuse_objects` view is the right one when something is
allocating *millions* of small things (e.g. deserialised strings).

Override the band size with `GALA_HEAP_DUMP_BAND_MB=<int>` (default 500).

### `GALA_PPROF=host:port`

Starts `net/http/pprof` on the given address. Useful when you want to
attach interactively while the process runs:

```bash
go tool pprof http://127.0.0.1:6060/debug/pprof/heap
go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=10
```

The endpoint is registered on the default mux via `_ "net/http/pprof"`,
so block and mutex profiles work out of the box.

### `GALA_PROFILE=1`

Enables per-phase timing logs from
`internal/transpiler/profiler` (parse, analyze, infer, transform,
generate, print). Look here first for "what's slow" — heap profiling is
overkill until phase timing tells you which phase is the suspect.

## When the disk cache eats space

Each analyzed package is gob-serialised under
`<project>/.gala/cache/v1[-<version-commit>]/<pkg>_<hash>.gob`.

```bash
du -sh <project>/.gala/cache
ls -la <project>/.gala/cache/v1*/ | sort -k5 -n | tail
```

A single package cache file should be a few KB to a few MB. Hundreds of
MB or more means a regression in `(*RichAST).Merge` or
`analyzer.cache.Put` is leaking transitive metadata back into per-package
artifacts. `rm -rf .gala/cache` is always safe — it forces a clean
re-analysis.

## Watchdog (Windows / WSL)

The runaway-process watchdog at
`gala_simple/.claude/gala_watchdog.sh` polls `tasklist` every 2 seconds
and `taskkill`s any `gala.exe` whose RSS exceeds the threshold (4 GB by
default — edit the script to change). Run alongside a build when you
suspect a leak so the host can't OOM:

```bash
bash .claude/gala_watchdog.sh 1800   # 30-minute window
```

## Capturing a profile under bazel

Bazel genrules run their tools in a sandboxed shell, so env vars must be
forwarded explicitly. Add to `.bazelrc` for the duration of an
investigation:

```
build --action_env=GALA_HEAP_DUMP_DIR=/tmp/heap
build --action_env=GALA_HEAP_DUMP_BAND_MB=500
```

Don't commit these — they belong in a developer's local `user.bazelrc`.
