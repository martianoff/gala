# GALA-E0128 — Go slice expression

**When it fires.** Part of a sequence is taken with Go's slice syntax:
`s[i:j]`, `s[i:]`, `s[:j]`, or the three-index `s[i:j:k]`. GALA has no slice
syntax.

**Minimal repro.**

```gala
package main

import "martianoff/gala/go_interop"

func main() {
    val xs = go_interop.SliceOf(1, 2, 3, 4)
    Println(xs[:2])
}
```

**Error output.**

```
error[GALA-E0128]: Go slice expression `xs[:2]`: GALA has no slice syntax
  --> main.gala:7:13
  |
7 |     Println(xs[:2])
  |             ^^^^^^ call a method instead
  |
  = hint: call a method instead; here, `xs.Take(2)` on an `Array` or a `List`, `go_interop.SliceTake(xs, 2)` on a Go slice
```

**Fix.** Call a method. On a GALA `Array` or `List`, `s[i:j]` is
`s.Slice(i, j)`, `s[i:]` is `s.Drop(i)` and `s[:j]` is `s.Take(j)`. On a Go
slice the `go_interop` functions of the same names do it:
`go_interop.Slice(s, i, j)`, `go_interop.SliceDrop(s, i)`,
`go_interop.SliceTake(s, j)`:

```gala
package main

import "martianoff/gala/go_interop"

func main() {
    val xs = go_interop.SliceOf(1, 2, 3, 4)
    Println(go_interop.SliceTake(xs, 2))
}
```

The hint spells both, with the operand and bounds written. Prefer GALA's
collections in GALA code, and keep Go slices for the boundary with Go
([Go interop](../GO_INTEROP.MD)).

**Rationale.** Slices are a Go interop type in GALA, reached through
`go_interop` rather than syntax, so no slice syntax will be added. The parser
used to report `mismatched input ':' expecting ']'`, which did not say where
the operation went. It is now this error, at the operand.

**Scope.** Indexing, `s[i]`, is valid GALA. Converting a string to bytes,
`[]byte(s)`, is [GALA-E0040](GALA-E0040.md).
