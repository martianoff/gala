---
layout: default
title: "GALA-E0036 — Bare Go Statement Keyword Is Not Part of GALA's Surface"
description: "\"bare Go statement keyword \"defer\" is not part of GALA's surface\" — GALA-E0036 rejects defer, go, goto, fallthrough, select and chan. See the real output and the `use` / resource / Future replacements."
keywords: "gala-e0036, bare go statement keyword, gala defer not allowed, gala use binding, gala resource combinators, gala go spawn, gala goto fallthrough select chan"
permalink: /docs/errors/gala-e0036/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0036</p>

# GALA-E0036 — Bare Go statement keyword is not part of GALA's surface

**What it means.** One of the Go-only statement keywords — `defer`, `go`, `goto`, `fallthrough`, `select`, `chan` — appears as a bare identifier statement.

None of these are GALA keywords, so the parser accepts them as an ordinary identifier expression-statement. They only ever produced working Go *by accident*: the final gofmt pass re-absorbed the following statement into a real Go statement, so `defer` on one line and `f.Close()` on the next glued together into a Go `DeferStmt`. That behaviour is undocumented and fragile — it depends entirely on what happens to follow — so a bare use is a hard error.

---

## Code that triggers it

The keyword is rejected on its own. No import or resource is needed, because the check runs before any symbol is resolved:

```gala
package main

func main() {
    defer
    Println("done")
}
```

---

## Compiler message

```
error[GALA-E0036]: bare Go statement keyword "defer" is not part of GALA's surface
  --> main.gala:4:5
  |
4 |     defer
  |     ^^^^^ GALA has no `defer`
  |
  = hint: GALA has no `defer`; use the `resource` combinators — `Using` / `Bracket` / `WithLock` from martianoff/gala/resource (or a `use x = ...` binding) — which guarantee cleanup on every exit path
```

In real code the offender is usually a Go-style `defer f.Close()`, where the keyword and the call sit on consecutive lines; the diagnostic points at the keyword either way.

---

## The replacement table

Every forbidden keyword, with the hint the compiler emits for it:

| Keyword | Hint the compiler emits |
|---|---|
| `defer` | GALA has no `defer`; use the `resource` combinators — `Using` / `Bracket` / `WithLock` from martianoff/gala/resource (or a `use x = ...` binding) — which guarantee cleanup on every exit path |
| `go` | GALA has no bare `go` statement; use `go_interop.Spawn(() => ...)` to start a goroutine |
| `goto` | GALA has no `goto`; use structured control flow — pattern matching, recursion, or a `for` loop |
| `fallthrough` | GALA has no `fallthrough`; `match` arms never fall through — combine patterns with `\|` or restructure the match |
| `select` | GALA has no `select` statement; use the go_interop channel helpers |
| `chan` | GALA has no bare `chan` statement; use the go_interop channel helpers to build and operate on channels |

---

## How to fix it — `defer` becomes `use`

`use x = acquire` binds `x` for the rest of the enclosing block and guarantees `x.Close()` runs when the function returns, on every path — normal return or panic. The resource must satisfy `Close() error`. No import is needed.

```gala
package main

struct Handle(name string)

func (h Handle) Close() error {
    Println(s"close ${h.name}")
    return nil
}

func open(name string) Handle = Handle(name)

func work() {
    use f = open("data.txt")
    Println(s"reading ${f.name}")
}

func main() {
    work()
    Println("done")
}
```

Multiple `use` bindings release **LIFO** — the last acquired closes first — so nested resources unwind in the right order. `use` lowers to Go `defer` in the generated code, which is the one sanctioned place Go's `defer` still exists.

When you need explicit control over acquire and release, or the resource does not implement `Close() error`, reach for the `resource` combinators (`Using` / `Bracket` / `WithLock`) instead.

## How to fix it — `go` becomes `Spawn`, or better `Future`

`go_interop.Spawn(() => ...)` starts a goroutine directly. Prefer `concurrent.Future` where you can: it is a checked concurrency boundary, so the transpiler verifies the closure only captures shareable state — see [GALA-E0037](/docs/errors/gala-e0037/). `Spawn` is the unchecked escape hatch.

The remaining four keywords have no GALA equivalent by design; the table's hints are the whole answer.

---

## Why the rule exists

A GALA program should not depend on the shape of the Go the transpiler happens to emit. The bare-keyword forms did exactly that: whether `defer` "worked" depended on gofmt re-absorbing the next statement, so adding a blank line or reordering two statements could silently change whether cleanup ran at all. Rejecting the bare form removes that coupling and points at constructs — `use`, the `resource` combinators, `Future` — that make the guarantee explicit and are checked.

**Scope.** Bare identifier statements only, and the check is **resolver-aware**: like the builtin check ([GALA-E0035](/docs/errors/gala-e0035/)), a name the program itself declared is that declaration, not a leaked keyword. A user-defined function, a local `val`/`var`/parameter, or a declared type named `select` is left alone. Anything with a postfix, operator, or argument list (`x.defer()`) is an ordinary expression and is not checked.

---

## Related

- [GALA-E0035](/docs/errors/gala-e0035/) — Go *builtins*, rejected under the same reasoning
- [GALA-E0037](/docs/errors/gala-e0037/) — the capture check `Future` gives you and `Spawn` does not
- [Concurrency](/features/concurrency/) — `Future`, `Promise`, `ExecutionContext`
- [All GALA error codes](/docs/errors/)
