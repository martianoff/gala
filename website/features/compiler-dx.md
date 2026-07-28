---
layout: default
title: "GALA Compiler DX — Framed Diagnostics, GALA Stack Traces, Guaranteed TCO"
description: "GALA's compiler works for you: Rust/Elm-style framed error diagnostics with a caret and a hint, runtime panics that report .gala source positions instead of generated Go, and guaranteed tail-call optimization for self-recursive if-expression functions."
keywords: "gala compiler diagnostics, rust style error messages, golang stack trace source map, transpiler line directives, golang tail call optimization, go tco, gala error codes, compiler developer experience"
permalink: /features/compiler-dx/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/">Features</a> / Compiler DX</p>

# Compiler DX — The Compiler Works For You

A transpiler sits between you and the Go toolchain, and that seam is where developer experience usually goes to die: errors that point at generated code, panics with line numbers from a file you never wrote, recursion that blows the stack because the intermediate language has no answer for it.

GALA closes all three. Diagnostics are framed with a caret and a fix. Panics report `foo.gala:12`. Self-tail-recursive functions become loops.

---

## Framed compile diagnostics

Errors from the GALA CLI are rendered Rust/Elm style: a coded header, the source locus, the offending line, a caret under the exact span, and a hint.

```gala
package main

func main() {
    val s = "hello"
    val n = len(s)
    Println(n)
}
```

```
error[GALA-E0035]: bare Go builtin "len(...)" is not part of GALA's surface
  --> bare_len.gala:5:13
  |
5 |     val n = len(s)
  |             ^^^ use `.Size()`
  |
  = hint: use `.Size()` (logical size — characters for strings) or `.ByteSize()` (raw bytes) instead of `len(...)`
```

Details that matter in a terminal:

- **Exact spans.** When the diagnostic carries a span, the caret is exactly as wide as the token. Otherwise the width is derived by scanning the token at the column — an identifier run, or a quoted string through its closing delimiter.
- **Alignment survives real source.** Tabs in the source are replicated in the caret indent rather than collapsed, and columns are rune-indexed, so a line containing `café` does not shift the caret.
- **Two levels of hint.** A terse first clause rides on the caret line; the full hint sits in the `= hint:` footer.
- **Color when it helps.** ANSI color is emitted only when stderr is a TTY and `NO_COLOR` is unset. With color off the output is byte-identical plain text — pipe it into a file and you get the same frame.
- **Multiple diagnostics** are rendered in sequence, separated by a blank line.
- **Go compiler errors pass through verbatim.** A `go build` failure is never re-framed or reinterpreted; you see exactly what the Go toolchain said.

Every coded error has a reference entry describing when it fires and how to fix it — see the [error-code reference]({{ '/docs/errors/' | relative_url }}), or [GALA-E0037]({{ '/features/concurrency-safety/#gala-e0037--the-diagnostic' | relative_url }}) for the data-race capture check.

---

## Stack traces map to GALA source

A runtime panic from a GALA program reports **GALA source positions**, not positions in the generated Go:

```gala
package main

func divide(a int, b int) int = a / b

func main() {
    Println("start")
    Println(divide(1, 0))
}
```

```
start
panic: runtime error: integer divide by zero

goroutine 1 [running]:
main.divide(...)
	/home/you/app/foo.gala:4
main.main()
	/home/you/app/foo.gala:7 +0x4c
```

Line 7 is the call, exactly. The `divide` frame lands one line past its declaration: `//line` is stamped on the `func` line, and the expression body `= a / b` becomes a separate `return` statement in the generated Go, so it inherits the next line — the "off by a few within the construct" caveat below, in its smallest form. (The directory prefix is shortened here for width; the real trace carries the full path the compiler was given.)

This works through Go's own `//line` directive mechanism: the transpiler stamps each statement and top-level declaration with its originating GALA line and emits a `//line <file>.gala:<n>` directive before the generated Go, which the Go compiler and runtime honor in panics and stack traces. There is **no separate source-map file and no runtime cost** — the directives only affect position metadata the compiler already records. A debugger's line numbers derive from the same information, so they follow too.

**Imported GALA packages map as well.** The standard library and third-party GALA dependencies are transpiled per file with their real source paths, so a panic originating inside an imported package reports *that library's* `.gala` position (e.g. `option.gala:37`) while the surrounding frames report your own files. A single stack trace can span library and application source, each mapped back to GALA.

**The honest accuracy caveat.** The mapping is exact for ordinary statements — one GALA statement to one Go statement. It is **approximate** — right file, line possibly off by a few within the construct — in three cases:

- statements that lower to several Go statements (e.g. `use x = acquire`, which becomes an assignment plus a deferred `Close()`), where only the first generated line sits under the directive;
- expression-bodied functions (`func f(…) T = expr`), where the directive sits on the `func` line and the generated `return expr` lands on the line after it; and
- IIFE-lowered constructs (`match`, `if`-expressions, `bind`/`also` do-notation), whose generated blocks are relocated relative to the source.

Directives are emitted only when the transpiler knows the source file path, so anonymous snippet transpilation (the LSP, for instance) emits none.

---

## Guaranteed tail-call optimization

Go has no tail-call guarantee, so a deeply self-recursive function normally grows the stack until it dies. GALA rewrites a **self-tail-recursive expression-bodied function into a `for {}` loop** in the generated Go, so it runs in constant stack space.

```gala
package main

func factorial(n int, acc int) int = if (n <= 0) acc else factorial(n - 1, n * acc)

func main() {
    Println(factorial(10, 1))
}
```

```
3628800
```

The payoff is depth. This recurses a million times and does not touch the stack:

```gala
package main

// sumTo adds every integer from n down to 1 onto acc using direct
// self-tail-recursion. Called with a large n, this would overflow a naive
// recursion's call stack; guaranteed TCO turns it into a constant-stack loop.
func sumTo(n int, acc int) int = if (n <= 0) acc else sumTo(n - 1, acc + n)

func main() {
    Println(sumTo(1000000, 0))
}
```

```
500000500000
```

The generated Go is the obvious loop: the condition becomes an `if`, the terminal branch a `return`, and the tail call evaluates every argument into temporaries **using the old parameter values** before reassigning all parameters simultaneously and hitting `continue`. So `sumTo(n - 1, acc + n)` reads the original `n` for both arguments, exactly as the recursion would.

### What is and is not rewritten

The restrictions are real, and worth knowing before you rely on the transform:

**Rewritten** — a plain function (no receiver) with an **expression body that is an `if`-expression**, whose branches are single expressions (nested `if`-expressions are followed), containing a **genuine direct self-tail-call**: the whole branch value is a call to the enclosing function itself, with positional plain arguments and matching arity.

**Not rewritten**, falling back to ordinary recursion:

| Shape | Why |
|-------|-----|
| Methods (anything with a receiver) | only plain functions are handled |
| `match`-form or block-body functions | those lower through an IIFE, where a `return` would return from the closure |
| A tail call inside a **block** branch — `if (c) { self(...) } else acc` | block branches are not scanned for tail calls |
| Non-tail recursion — `n * factorial(n - 1)` | the call is not the branch value; the multiplication happens after it returns |
| Mutual recursion (`isEven`/`isOdd`) | only direct self-calls are detected |
| Explicit type arguments — `self[T](...)` | not supported by the rewrite |
| Named or spread arguments | not supported by the rewrite |
| An argument containing a closure | the closure would capture loop-carried parameters by reference and observe them mutated on the next iteration |
| Variadic or multi-name parameters | the parameter list must be simple positional bindings |

The rewrite is **silent**: a function outside the supported shape compiles to ordinary Go recursion with unchanged behavior, and nothing warns you that it was not optimized. When constant stack space is a correctness requirement — not just a nicety — write the function in the supported shape and keep it there.

---

## Guardrails instead of accidents

The same "compiler works for you" principle applies to constructs that used to *appear* to work. Bare Go builtins and bare Go statement keywords are now hard errors with a named replacement, rather than silently doing something surprising.

**Bare Go builtins (GALA-E0035).** `len`, `append`, `make`, `new`, `cap`, `copy`, `delete`, `close`, `complex`, `real`, `imag`, `panic`, `recover` are not on GALA's surface. Each rejection names its replacement — `go_interop.SliceAppend`, `go_builtins.Panic`, `Try(() => ...)` for `recover`, and `.Size()` / `.ByteSize()` for `len`.

`len` in particular fixes a real footgun: Go's `len(string)` returns **bytes**, so any non-ASCII text mis-counts.

```gala
val chars = "héllo".Size()      // 5  (characters)
val bytes = "héllo".ByteSize()  // 6  (raw bytes)
```

**Bare Go statement keywords (GALA-E0036).** `defer`, `go`, `goto`, `fallthrough`, `select`, and `chan` are not part of GALA's grammar. They previously appeared to work only as an accident of the final gofmt pass, which glued the following statement onto the keyword — an undocumented, fragile artifact. The replacements:

| Forbidden statement | Use instead |
|---|---|
| `defer x.Close()` | a `use x = …` binding, or a `resource` combinator (`Using` / `Bracket` / `WithLock`) |
| `go f()` | `go_interop.Spawn(() => f())` |
| `goto`, `fallthrough`, `select`, `chan` | structured control flow and pattern matching; `go_interop` channel helpers |

Both checks are **resolver-aware**: a symbol the program itself declared — a `func delete(...)` of your own, a variable named `select` — is that declaration, not the builtin, and is left untouched.

### `use` — scoped resource binding

`use x = acquire` binds `x` for the rest of the enclosing block and guarantees `x.Close()` runs when the function returns, on every path including a panic. It is the top-to-bottom replacement for Go's `defer x.Close()`; the resource must satisfy `Close() error`. Multiple `use` bindings release **LIFO**, so nested resources unwind in the right order. No import is needed.

```gala
// A Closeable resource that announces open/close so ordering is observable.
type Handle struct {
    name string
}

func (h Handle) Close() error {
    Println(s"close ${h.name}")
    return nil
}

func open(name string) Handle {
    Println(s"open ${name}")
    return Handle(name = name)
}

// `a` is acquired first so it closes LAST; `b` closes FIRST.
func work() {
    use a = open("a")
    use b = open("b")
    Println(s"body sees ${a.name} and ${b.name}")
}
```

```
open a
open b
body sees a and b
close b
close a
```

`use` lowers to Go `x := acquire` plus `defer x.Close()` in the generated code — so Go's `defer` still exists, but only as this sanctioned internal lowering, never on the GALA surface.

---

## Further Reading

- [Compile-Time Data-Race Safety]({{ '/features/concurrency-safety/' | relative_url }}) — GALA-E0037, the strongest example of a runtime bug moved to compile time
- [Concurrency]({{ '/features/concurrency/' | relative_url }}) — `Future[T]`, cancellation, and structured concurrency
- [IDE Support]({{ '/features/ide-support/' | relative_url }}) — the same diagnostics, live in your editor
- [Pattern Matching]({{ '/features/pattern-matching/' | relative_url }}) — exhaustiveness checking, another compile-time guarantee
