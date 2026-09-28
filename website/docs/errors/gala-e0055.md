---
layout: default
title: "GALA-E0055 — Go Keyword Used as a Name"
description: "\"default\" is a Go keyword and cannot be used as a name — GALA-E0055 fires when a val, parameter, field, function, type or import is named break, chan, const, continue, default, defer, fallthrough, go, goto, select or switch."
keywords: "gala-e0055, gala go keyword, gala reserved word, gala cannot be used as a name, gala val default, gala func go, gala identifier keyword"
permalink: /docs/errors/gala-e0055/
last_modified_at: 2026-09-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0055</p>

# GALA-E0055 — Go keyword used as a name

**When it fires.** A name is spelled like one of Go's keywords that GALA does
not reserve itself: `break`, `chan`, `const`, `continue`, `default`, `defer`,
`fallthrough`, `go`, `goto`, `select` or `switch`.

GALA compiles to Go, and every name a GALA program declares becomes a name in
the generated Go. Go's other keywords (`func`, `type`, `map`, `if`, `for`, ...)
are GALA keywords too, so the parser already rejects them as names. These
eleven are not, so they parse as ordinary identifiers — and the Go they
produced did not.

The check covers every place a name is introduced: a `val`, `var` or `:=`
binding, a tuple destructuring, a parameter or lambda parameter, a pattern
binding (`case go =>`, `Some(go)`), a struct or sealed-case field, a function or
method, an interface method, a type, type alias, sealed type or sealed case, a
type parameter, the package name, and an import alias. It points at the
declaration, even when a use of the name comes first in the file. A use with no
declaration, such as `Println(default)`, is reported where it stands.

**Minimal repro.**

```gala
package main

func main() {
    val default = 8080
    Println(s"listening on ${default}")
}
```

**Error output.**

```
error[GALA-E0055]: "default" is a Go keyword and cannot be used as a name
  --> main.gala:4:9
  |
4 |     val default = 8080
  |         ^^^^^^^ rename it
  |
  = hint: rename it; GALA compiles to Go, where "default" is reserved, so nothing a GALA program declares can have that name
```

A function called above its declaration is reported at the declaration, where
the rename starts:

```gala
package main

func main() {
    go("build")
}

func go(task string) {
    Println(s"running ${task}")
}
```

```
error[GALA-E0055]: "go" is a Go keyword and cannot be used as a name
  --> main.gala:7:6
  |
7 | func go(task string) {
  |      ^^ rename it
  |
  = hint: rename it; GALA compiles to Go, where "go" is reserved, so nothing a GALA program declares can have that name
```

**Fix.** Pick another name and use it everywhere:

```gala
package main

func run(task string) {
    Println(s"running ${task}")
}

func main() {
    val defaultPort = 8080
    Println(s"listening on ${defaultPort}")
    run("build")
}
```

**What still works.**

- A bare `break` or `continue` statement inside a `for` loop is loop control,
  not a name.
- A bare `defer`, `go`, `goto`, `fallthrough`, `select` or `chan` statement is
  reported by [GALA-E0036](/docs/errors/gala-e0036/), which names the GALA replacement.
- A name that only contains a keyword — `defaultPort`, `goNow`, `selected` — is
  an ordinary name.
- Go's *predeclared* identifiers (`int`, `string`, `error`, `len`, `min`, `max`,
  `copy`, ...) are not keywords. A binding may shadow one, as in Go.

**Rationale.** These names used to be emitted into the generated Go unchanged.
Go rejected the file before it could report anything about the source, so the
author got the internal [GALA-E0017](/docs/errors/gala-e0017/) — "the generated Go is not
parseable" — instead of a diagnostic about their own code.

GALA rejects the name rather than renaming it behind the author's back. A
renamed local (`go` to `go_`) would be harmless, but a renamed struct field,
exported function or package-level `val` is visible to Go code, to JSON field
names and to reflection under a name the author never wrote. And `break` and
`continue` already mean loop control as statements, so a binding with either
name could not be referred to unambiguously. A rule with no exceptions is the
one that is easy to predict.

**Scope.** Names in the file being compiled. Go keywords that are also GALA
keywords never reach this check: the parser rejects them first, with a syntax
error.
