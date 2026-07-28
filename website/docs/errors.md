---
layout: default
title: "GALA Compiler Error Codes — Complete Reference (GALA-E0001 to GALA-E0037)"
description: "Every GALA compile-time error code explained: what it means, the code that triggers it, the exact compiler message, and how to fix it. Searchable reference for GALA-E0001 through GALA-E0037."
keywords: "gala error codes, gala compiler errors, gala-e0001, gala-e0037, gala semantic error, gala transpiler error, gala compile error reference, golang transpiler error codes"
permalink: /docs/errors/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Error Codes</p>

# GALA Compiler Error Codes

Every semantic error the GALA transpiler emits carries a stable error code of the form `GALA-Exxxx`. Codes are opaque identifiers — they never change meaning across releases, so tools, tests, CI checks, and documentation can link against them.

If the compiler handed you a code, find it in the table below and jump straight to the page for it.

---

## Anatomy of a GALA error

`gala build`, `gala run`, and `gala transpile` print a framed diagnostic:

```
error[GALA-E0002]: non-exhaustive match: missing cases: Blue
  --> main.gala:7:29
  |
7 | func name(c Color) string = c match {
  |                             ^ add the missing variant cases, or add a `case _ => ...` defa…
  |
  = hint: add the missing variant cases, or add a `case _ => ...` default to cover them
```

| Part | Meaning |
|------|---------|
| `error[GALA-E0002]` | The stable code. Search it here, or grep for it in CI output. |
| header text | What invariant was violated. |
| `--> main.gala:7:29` | File, line, and column of the offending token. |
| `^^^` | The span of the offending token, with a condensed hint beside it. |
| `= hint: …` | The full remediation hint. Not every code emits one. |

Diagnostics raised before a source position is known — package resolution and the type-inference engine — print the header and hint with no frame.

Your editor shows the same diagnostic in a terse single-line form (`[SemanticError GALA-E0002] main.gala:7:28 …`), because the language server consumes that shape. Same code, same message. See [Compiler DX](/features/compiler-dx/) for more on the diagnostic renderer.

---

## All error codes

| Code | What it means | Category |
|------|---------------|----------|
| [GALA-E0001](/docs/errors/gala-e0001/) | Recursive `Immutable[T]` wrap | Immutability |
| [GALA-E0002](/docs/errors/gala-e0002/) | Non-exhaustive match on a sealed type | Pattern matching |
| [GALA-E0003](/docs/errors/gala-e0003/) | Match expression missing a default case | Pattern matching |
| [GALA-E0004](/docs/errors/gala-e0004/) | Sealed variant pattern binds the wrong number of fields | Pattern matching |
| [GALA-E0005](/docs/errors/gala-e0005/) | Extractor has no `Unapply` method | Pattern matching |
| [GALA-E0006](/docs/errors/gala-e0006/) | Multiple default cases in one match | Pattern matching |
| [GALA-E0007](/docs/errors/gala-e0007/) | Slice literal `[]T{…}` is not a GALA construct | Collections |
| [GALA-E0008](/docs/errors/gala-e0008/) | Map literal `map[K]V{…}` is not a GALA construct | Collections |
| [GALA-E0009](/docs/errors/gala-e0009/) | Unrecognized pattern syntax (transpiler bug) | Internal |
| [GALA-E0010](/docs/errors/gala-e0010/) | Sibling `.gala` files declare different package names | Packages |
| [GALA-E0011](/docs/errors/gala-e0011/) | Type redefined in the same package | Declarations |
| [GALA-E0012](/docs/errors/gala-e0012/) | Method redefined on the same type | Declarations |
| [GALA-E0013](/docs/errors/gala-e0013/) | Non-defaulted parameter follows a defaulted one | Declarations |
| [GALA-E0014](/docs/errors/gala-e0014/) | Default value type does not match the parameter type | Declarations |
| [GALA-E0015](/docs/errors/gala-e0015/) | Bare `return` inside a value-producing match | Pattern matching |
| [GALA-E0016](/docs/errors/gala-e0016/) | Generic struct field name collides with a generic type name | Declarations |
| [GALA-E0017](/docs/errors/gala-e0017/) | Internal transpiler panic | Internal |
| [GALA-E0018](/docs/errors/gala-e0018/) | Cannot infer the type parameter of a sealed variant constructor | Type inference |
| [GALA-E0019](/docs/errors/gala-e0019/) | Empty parenthesized expression `()` used as a value | Expressions |
| [GALA-E0020](/docs/errors/gala-e0020/) | Package not found | Packages |
| [GALA-E0021](/docs/errors/gala-e0021/) | Type mismatch (unification failure) | Type inference |
| [GALA-E0022](/docs/errors/gala-e0022/) | Occurs check failed (infinite type) | Type inference |
| [GALA-E0023](/docs/errors/gala-e0023/) | Undefined variable | Type inference |
| [GALA-E0024](/docs/errors/gala-e0024/) | Internal inference failure (transpiler bug) | Internal |
| [GALA-E0025](/docs/errors/gala-e0025/) | Unresolved cross-package symbol (missing import) | Packages |
| [GALA-E0033](/docs/errors/gala-e0033/) | Lambda parameter has no type and none can be inferred | Type inference |
| [GALA-E0034](/docs/errors/gala-e0034/) | Function/struct parameter has no declared type | Declarations |
| [GALA-E0037](/docs/errors/gala-e0037/) | Non-shareable value crosses a goroutine boundary | Concurrency safety |

Code numbering has gaps. Codes are never renumbered, so a missing number simply means that code has no dedicated page — see below.

---

## Codes without a dedicated page

These codes exist in the compiler and can appear in your build output, but they have no reference page yet. The one-line descriptions come from the compiler's own code-table; the pages above are the ones with worked examples.

| Code | What it means |
|------|---------------|
| `GALA-E0026` | An unqualified sealed-variant constructor name matches a case in two or more dot-imported sealed types. Qualify the call site (`pkg.Variant(...)`) to disambiguate. |
| `GALA-E0027` | A top-level function with the same name is declared more than once in a package. |
| `GALA-E0028` | A type alias (`type Foo = Bar`) with the same name is declared more than once in the same file. |
| `GALA-E0029` | An interface lists two method specs with the same name. |
| `GALA-E0030` | A struct declares two fields with the same name. |
| `GALA-E0031` | A sealed type lists two `case` variants with the same name. |
| `GALA-E0032` | Two dot-imported packages export the same identifier. Qualify or alias one of the imports. |
| `GALA-E0035` | A bare Go builtin (`len`, `append`, `make`, `new`, `cap`, `copy`, `delete`, `close`, `complex`, `real`, `imag`, `panic`, `recover`) was called as a function. Each has a GALA-native or `go_interop` replacement — for example `.Size()` / `.ByteSize()` instead of `len(...)`. |
| `GALA-E0036` | A Go-only statement keyword (`defer`, `go`, `goto`, `fallthrough`, `select`, `chan`) appeared as a bare statement. GALA expresses cleanup with the `resource` combinators and goroutines with `go_interop.Spawn`. |

---

## By category

**Pattern matching and sealed types** — [E0002](/docs/errors/gala-e0002/), [E0003](/docs/errors/gala-e0003/), [E0004](/docs/errors/gala-e0004/), [E0005](/docs/errors/gala-e0005/), [E0006](/docs/errors/gala-e0006/), [E0015](/docs/errors/gala-e0015/), [E0018](/docs/errors/gala-e0018/). Start with [Pattern Matching](/features/pattern-matching/) and [Sealed Types](/features/sealed-types/).

**Collections and Go interop** — [E0007](/docs/errors/gala-e0007/), [E0008](/docs/errors/gala-e0008/). See [Collections](/features/collections/) and [Go Interop](/features/go-interop/).

**Declarations** — [E0011](/docs/errors/gala-e0011/), [E0012](/docs/errors/gala-e0012/), [E0013](/docs/errors/gala-e0013/), [E0014](/docs/errors/gala-e0014/), [E0016](/docs/errors/gala-e0016/), [E0034](/docs/errors/gala-e0034/).

**Type inference** — [E0018](/docs/errors/gala-e0018/), [E0021](/docs/errors/gala-e0021/), [E0022](/docs/errors/gala-e0022/), [E0023](/docs/errors/gala-e0023/), [E0033](/docs/errors/gala-e0033/). See [Type Inference](/features/type-inference/).

**Packages and imports** — [E0010](/docs/errors/gala-e0010/), [E0020](/docs/errors/gala-e0020/), [E0025](/docs/errors/gala-e0025/). See [Dependency Management](/docs/dependency-management/).

**Immutability and concurrency safety** — [E0001](/docs/errors/gala-e0001/), [E0037](/docs/errors/gala-e0037/). See [Immutability](/features/immutability/) and [Concurrency Safety](/features/concurrency-safety/).

**Transpiler bugs** — [E0009](/docs/errors/gala-e0009/), [E0017](/docs/errors/gala-e0017/), [E0024](/docs/errors/gala-e0024/). These mean the transpiler hit a case it does not handle; please [file an issue](https://github.com/martianoff/gala/issues) with the source snippet.

---

## Further Reading

- [Documentation Hub](/docs/) — language reference, standard library, and guides
- [Compiler DX](/features/compiler-dx/) — framed diagnostics, carets, and GALA source positions in stack traces
- [Pattern Matching](/features/pattern-matching/) — exhaustive matching, destructuring, and guards
- [Sealed Types](/features/sealed-types/) — closed hierarchies with compile-time exhaustiveness
- [Concurrency Safety](/features/concurrency-safety/) — the shareability model behind GALA-E0037
- [Error Handling](/features/error-handling/) — `Option`, `Either`, and `Try` for *runtime* failures (compile-time codes are on this page)
