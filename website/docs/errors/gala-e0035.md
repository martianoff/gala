---
layout: default
title: "GALA-E0035 — Bare Go Builtin Is Not Part of GALA's Surface"
description: "\"bare Go builtin \"len(...)\" is not part of GALA's surface\" — GALA-E0035 rejects len, append, make, panic and the rest. See the real compiler output and the full replacement table."
keywords: "gala-e0035, bare go builtin, gala len not allowed, gala size bytesize, gala append make new, gala panic recover, go_interop replacements"
permalink: /docs/errors/gala-e0035/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0035</p>

# GALA-E0035 — Bare Go builtin is not part of GALA's surface

**What it means.** A Go builtin (`len`, `append`, `make`, `new`, `cap`, `copy`, `delete`, `close`, `complex`, `real`, `imag`, `panic`, `recover`) is called as a bare function.

These were the last symbols that resolved with **no import and no GALA declaration** — an implicit Go leakage the language removes. Each one has a GALA-native form or a sanctioned interop wrapper, so a bare call is a hard error rather than a warning.

---

## Code that triggers it

```gala
package main

func main() {
    val s = "héllo"
    val n = len(s)
    Println(n)
}
```

---

## Compiler message

```
error[GALA-E0035]: bare Go builtin "len(...)" is not part of GALA's surface
  --> main.gala:5:13
  |
5 |     val n = len(s)
  |             ^^^ use `.Size()`
  |
  = hint: use `.Size()` (logical size — characters for strings) or `.ByteSize()` (raw bytes) instead of `len(...)`
```

The caret underlines the callee identifier exactly — the diagnostic carries a precise span rather than letting the renderer guess the token width. The caret row shows a shortened hint; the full text is on the `= hint:` line.

---

## The replacement table

Every forbidden builtin, with the hint the compiler emits for it:

| Builtin | Hint the compiler emits |
|---|---|
| `len` | use `.Size()` (logical size — characters for strings) or `.ByteSize()` (raw bytes) instead of `len(...)` |
| `append` | use `go_interop.SliceAppend` / `SliceAppendAll`, or build an `Array`/`List` from collection_immutable |
| `make` | use `go_interop.SliceWithSize` / `SliceWithCapacity` / `MapEmpty`, or an empty `Array`/`HashMap` |
| `new` | use `go_interop.New[T]()` for a pointer, or a zero value / `Option[T]` |
| `cap` | use `go_interop.SliceCap(...)` |
| `copy` | use `go_interop.SliceCopy(...)`, or copy an `Array` |
| `delete` | use `go_interop.MapDelete(...)`, or `HashMap.Remove(...)` |
| `close` | use `go_interop.CloseChan(...)` (or `CloseSignal(...)` for a signal channel) |
| `complex` | use `go_interop.Complex(...)` |
| `real` | use `go_interop.Real(...)` |
| `imag` | use `go_interop.Imag(...)` |
| `panic` | use `go_builtins.Panic(...)` — or prefer `Option` / `Try` / `Either` for recoverable failure |
| `recover` | `recover` is not available on the GALA surface; `Try` captures panics — use `Try(() => ...)` / `TryApply` |

`panic` is the one most likely to bite an existing codebase: a bare `panic("unreachable")` in a match arm is now GALA-E0035. Import `martianoff/gala/go_builtins` and call `go_builtins.Panic(...)`.

---

## How to fix it

Use the GALA-native form. For `len`, that is `.Size()` or `.ByteSize()`:

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val s = "héllo"
    Println(s.Size())      // 5 — characters
    Println(s.ByteSize())  // 6 — raw bytes

    val nums = ArrayOf(1, 2, 3)
    Println(nums.Size())   // 3
}
```

**Pick deliberately.** Go's `len(string)` returns **bytes**, so non-ASCII text mis-counts. Use `.Size()` when you mean "how many things"; use `.ByteSize()` only for genuine byte-level work — indexing a byte buffer, computing an offset, sizing an I/O read. Using `.Size()` as the bound of a **byte** loop truncates multibyte input, which is the exact bug `len` used to cause silently.

---

## Why the rule exists

GALA's rule is that every symbol resolves through an import, a GALA declaration, or the std prelude. The Go builtins were the one exception: they resolved out of nowhere because the generated Go happened to have them in scope. That is invisible coupling to the target language, and it hurt most in the `len` case, where the leaked builtin silently had the wrong semantics for text. Forbidding the bare form removes the special case and routes each operation to something with a name that says what it does.

**Scope.** Bare calls only, and the check is **resolver-aware**: a name you declared yourself is your declaration, not the builtin. A user-defined `func delete(...)` or `func copy(...)` is left alone, as is a local `val`/`var`/parameter or a declared type with one of these names. A *selector* call is never a bare builtin either — `x.copy()` is a method call and is not checked.

---

## Related

- [GALA-E0036](/docs/errors/gala-e0036/) — Go-only *statement* keywords, rejected under the same reasoning
- [GALA-E0007](/docs/errors/gala-e0007/) — slice literals, another Go construct with no GALA surface
- [Collections](/features/collections/) — `.Size()`, `.ByteSize()`, and the immutable collection API
- [Go Interop](/features/go-interop/) — the sanctioned wrappers named in the table
- [All GALA error codes](/docs/errors/)
