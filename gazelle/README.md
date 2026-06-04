# gala_gazelle — a Gazelle extension for GALA

`gala_gazelle` teaches [Gazelle](https://github.com/bazelbuild/bazel-gazelle)
to generate and maintain BUILD targets for [GALA](https://github.com/martianoff/gala)
source. It manages `gala_library`, `gala_binary`, `gala_test`, and
`gala_exec_test` rules (loaded from `@rules_gala//gala:defs.bzl`) and resolves
GALA `import` strings to Bazel labels.

Import extraction is delegated to a small helper binary — `gala imports --json`
— instead of re-implementing the ANTLR grammar, mirroring the helper model used
by the rules_python Gazelle plugin.

## Adding the extension (consumer setup)

This is a standalone Bazel module. In your `MODULE.bazel`:

```starlark
bazel_dep(name = "gala_gazelle", version = "0.1.0")
```

Then build a `gazelle_binary` that bundles the GALA language alongside any
others you use, and an invocation target, in a `BUILD.bazel`:

```starlark
load("@gazelle//:def.bzl", "gazelle", "gazelle_binary")

gazelle_binary(
    name = "gazelle_bin",
    languages = [
        "@gazelle//language/go",      # optional: keep Go support
        "@gala_gazelle//gala",        # GALA support
    ],
)

# gazelle:gala_prefix github.com/you/project
gazelle(
    name = "gazelle",
    gazelle = ":gazelle_bin",
)
```

Run it with `bazel run //:gazelle`.

The helper binary must be discoverable. By default the extension shells out to
`gala` on `PATH`; override with the `-gala_helper` flag or the
`# gazelle:gala_helper` directive (see below).

## What it generates

Per directory containing `.gala` files:

- **`gala_library`** for the non-test sources, with `importpath` derived from
  the configured prefix plus the directory's repo-relative path, and
  `visibility = ["//visibility:public"]`.
- **`gala_binary`** instead of a library when a source declares `package main`
  and defines a zero-argument `main()`.
- **`gala_test`** for the directory's `*_test.gala` files.

`srcs` are sorted; `deps` are resolved from each file's GALA imports.

## Dependency resolution

For every GALA import in a rule's sources:

- **In-repo packages** (under the `gala_prefix`) resolve to in-repo labels —
  preferring an indexed `gala_library` with a matching `importpath`, otherwise
  `//<relpath>`.
- **GALA stdlib / external packages** (under `gala_stdlib_prefix`, default
  `martianoff/gala`) resolve to the matching in-repo package if one exists, and
  otherwise to `@<gala_stdlib_repo>//<relpath>` (default repo `gala`).
- **Go stdlib and other non-GALA imports** (`fmt`, `strings`, …) are skipped.
- **Macro-injected deps** (`martianoff/gala/std`, `martianoff/gala/test`) are
  skipped — the `gala_*` macros add them automatically, so emitting them in
  `deps` would be redundant.

## Directives

| Directive | Default | Meaning |
|-----------|---------|---------|
| `# gazelle:gala_prefix <path>` | `martianoff/gala` | Import-path prefix for in-repo packages. |
| `# gazelle:gala_helper <path>` | `gala` | Path to the import-extraction helper binary. |
| `# gazelle:gala_stdlib_prefix <path>` | `martianoff/gala` | Import namespace of the GALA standard library. |
| `# gazelle:gala_stdlib_repo <name>` | `gala` | Bazel external repo for out-of-repo stdlib imports. |
| `# gazelle:gala_implicit_dep <paths…>` | `martianoff/gala/std martianoff/gala/test` | Space-separated import paths the macros inject automatically (excluded from `deps`). `none` clears the set. |

Flags `-gala_prefix` and `-gala_helper` mirror the first two directives.

## Helper contract

The extension invokes `<helper> imports --json <files…>` (working directory =
the package directory) and parses a JSON array:

```json
[
  {
    "file": "a.gala",
    "package": "main",
    "imports": [{"path": "fmt", "alias": "", "dot": false}],
    "error": ""
  }
]
```

Entries with a non-empty `"error"` are skipped (the helper could not parse that
file).

## Development

```sh
cd gazelle
go test ./...      # unit tests (testdata-driven Generate + Resolve)
```

Unit tests inject a fake helper that emits the JSON contract directly, so they
run without an installed `gala` binary.
