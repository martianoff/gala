---
layout: default
title: "GALA-E0119 — Go Directive in a GALA File"
description: "GALA-E0119 fires on a Go compiler directive such as //go:embed or //go:build in a .gala file, which GALA does not carry into the generated Go. Embed with embed val; keep build constraints in .go files."
keywords: "gala-e0119, gala go:embed, gala go:build, gala build tags, gala build constraint, gala embed val, gala directive"
permalink: /docs/errors/gala-e0119/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0119</p>

# GALA-E0119 — Go directive in a GALA file

**When it fires.** A line comment in a `.gala` file is a Go compiler
directive: a `//go:` line such as `//go:embed`, `//go:build` or
`//go:noinline`, or a legacy `// +build` constraint. GALA does not carry
comments into the generated Go as directives, so the line would do nothing.

**Minimal repro.**

```gala
package main

//go:embed hello.txt
var greeting string

func main() {
    Println(greeting)
}
```

**Error output.**

```
error[GALA-E0119]: Go directive `//go:embed hello.txt` in a GALA file: GALA does not carry it into the generated Go
  --> main.gala:3:1
  |
3 | //go:embed hello.txt
  | ^^^^^^^^^^^^^^^^^^^^ embed with `embed val`
  |
  = hint: embed with `embed val`; here, `embed val greeting = "hello.txt"` for a string, or `embed val greeting EmbeddedFS = "hello.txt"` for a file system
```

**Fix.** Embed a file with GALA's `embed val`, which takes the pattern as a
string, makes the transpiler emit the `//go:embed` line, and binds an immutable
value:

```gala
embed val greeting = "hello.txt"
```

A directory, a glob or several patterns embed as an `EmbeddedFS`:
`embed val static EmbeddedFS = "static/*"`. See
[Embedding Files](/docs/language-reference/#14-embedding-files).

A build constraint has no GALA form either:

```gala
//go:build linux

package main

func main() {
    Println("linux only")
}
```

```
error[GALA-E0119]: Go directive `//go:build linux` in a GALA file: GALA does not carry it into the generated Go
  --> main.gala:1:1
  |
1 | //go:build linux
  | ^^^^^^^^^^^^^^^^ a GALA file has no build constraint
  |
  = hint: a GALA file has no build constraint; put the code for one platform in a .go file of the package, which keeps its `//go:build` line
```

Put the code for each platform in a hand-written `.go` file of the package,
which keeps its `//go:build` line — `name_linux.go` and `name_windows.go`, each
declaring `platformName()` — and call it from GALA; or choose at run time with
`runtime.GOOS`. The same holds for every other directive (`//go:noinline`,
`//go:linkname`, `//go:generate`, ...): the code that needs it goes in a `.go`
file ([Building a mixed project](/docs/go-interop/#part-4-building-a-mixed-project)).

**Rationale.** The directive used to be dropped without a word. A
`//go:embed` variable stayed empty at run time, and a file constrained to
Linux built and ran on Windows too. Nothing in the build said why.

**Scope.** As in Go, only a `//` line comment that starts its line is a
directive: `//go:` with no space and a name after it, or `// +build`. A
comment that mentions a directive (`// use embed val, not //go:embed`),
`// go:` with a space, a comment after code on its line, and a block comment
are ordinary comments.
