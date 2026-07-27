# GALA Compile-Time Error Codes

Every semantic error the GALA transpiler emits carries a stable error code of
the form `GALA-Exxxx`. Codes are opaque identifiers — they never change
meaning across releases, so tools, tests, and documentation can link against
them.

Each code has a dedicated documentation page in this directory explaining:
- when the transpiler emits it
- a minimal repro
- how to fix it
- the underlying design rationale (so you can judge edge cases)

## Index

| Code | Title | Page |
|------|-------|------|
| `GALA-E0001` | Recursive `Immutable` wrap | [GALA-E0001.md](GALA-E0001.md) |
| `GALA-E0002` | Non-exhaustive sealed match | [GALA-E0002.md](GALA-E0002.md) |
| `GALA-E0003` | Missing default case | [GALA-E0003.md](GALA-E0003.md) |
| `GALA-E0004` | Sealed variant arity mismatch | [GALA-E0004.md](GALA-E0004.md) |
| `GALA-E0005` | Missing `Unapply` on extractor | [GALA-E0005.md](GALA-E0005.md) |
| `GALA-E0006` | Multiple default cases | [GALA-E0006.md](GALA-E0006.md) |
| `GALA-E0007` | Slice literal not supported | [GALA-E0007.md](GALA-E0007.md) |
| `GALA-E0008` | Map literal not supported | [GALA-E0008.md](GALA-E0008.md) |
| `GALA-E0009` | Unknown pattern type | [GALA-E0009.md](GALA-E0009.md) |
| `GALA-E0010` | Duplicate package name in directory | [GALA-E0010.md](GALA-E0010.md) |
| `GALA-E0011` | Type redefined | [GALA-E0011.md](GALA-E0011.md) |
| `GALA-E0012` | Method redefined | [GALA-E0012.md](GALA-E0012.md) |
| `GALA-E0013` | Non-defaulted parameter after defaulted parameter | [GALA-E0013.md](GALA-E0013.md) |
| `GALA-E0014` | Default expression type mismatch | [GALA-E0014.md](GALA-E0014.md) |
| `GALA-E0015` | Bare `return` inside a value-producing match | [GALA-E0015.md](GALA-E0015.md) |
| `GALA-E0016` | Struct field name collides with type name | [GALA-E0016.md](GALA-E0016.md) |
| `GALA-E0017` | Internal transpiler panic | [GALA-E0017.md](GALA-E0017.md) |
| `GALA-E0018` | Sealed variant type parameter cannot be inferred | [GALA-E0018.md](GALA-E0018.md) |
| `GALA-E0019` | Empty parenthesized expression | [GALA-E0019.md](GALA-E0019.md) |
| `GALA-E0020` | Package not found | [GALA-E0020.md](GALA-E0020.md) |
| `GALA-E0021` | Type mismatch (unification failure) | [GALA-E0021.md](GALA-E0021.md) |
| `GALA-E0022` | Occurs check failure | [GALA-E0022.md](GALA-E0022.md) |
| `GALA-E0023` | Undefined variable | [GALA-E0023.md](GALA-E0023.md) |
| `GALA-E0024` | Internal inference failure | [GALA-E0024.md](GALA-E0024.md) |
| `GALA-E0025` | Unresolved cross-package symbol | [GALA-E0025.md](GALA-E0025.md) |
| `GALA-E0026` | Ambiguous sealed-variant reference | [GALA-E0026.md](GALA-E0026.md) |
| `GALA-E0027` | Function redeclared | [GALA-E0027.md](GALA-E0027.md) |
| `GALA-E0028` | Type alias redeclared | [GALA-E0028.md](GALA-E0028.md) |
| `GALA-E0029` | Interface method redeclared | [GALA-E0029.md](GALA-E0029.md) |
| `GALA-E0030` | Struct field redeclared | [GALA-E0030.md](GALA-E0030.md) |
| `GALA-E0031` | Sealed variant case redeclared | [GALA-E0031.md](GALA-E0031.md) |
| `GALA-E0032` | Dot-import symbol collision | [GALA-E0032.md](GALA-E0032.md) |
| `GALA-E0033` | Untyped lambda parameter | [GALA-E0033.md](GALA-E0033.md) |
| `GALA-E0034` | Untyped function/struct parameter | [GALA-E0034.md](GALA-E0034.md) |
| `GALA-E0035` | Bare Go builtin is forbidden | [GALA-E0035.md](GALA-E0035.md) |
| `GALA-E0036` | Bare Go statement keyword is forbidden | [GALA-E0036.md](GALA-E0036.md) |
| `GALA-E0037` | Unshareable capture across a concurrency boundary | [GALA-E0037.md](GALA-E0037.md) |
| `GALA-E0038` | Invalid string escape sequence | [GALA-E0038.md](GALA-E0038.md) |

Every code defined in `galaerr/errors.go` now has a page. Three of them describe
conditions no valid source can reach — [GALA-E0009](GALA-E0009.md),
[GALA-E0017](GALA-E0017.md), and [GALA-E0024](GALA-E0024.md) signal a
**transpiler defect**, and the right response is a bug report, not a source
change. Three more originate in the type-inference engine and are **not
currently surfaced** to users at all: [GALA-E0021](GALA-E0021.md),
[GALA-E0022](GALA-E0022.md), and [GALA-E0024](GALA-E0024.md) — type errors of
that class are reported by the Go compiler against the generated Go.
[GALA-E0026](GALA-E0026.md) is a defensive check that
[GALA-E0032](GALA-E0032.md) reports first in every construction found so far,
and [GALA-E0020](GALA-E0020.md) surfaces nested inside an unresolved-imports
wrapper rather than as a standalone framed diagnostic. Each page says so
explicitly.

## Adding a new code

1. Append a new constant to `galaerr/errors.go` — never renumber existing codes.
2. Emit the error via `galaerr.NewCodedSemanticError(code, line, col, msg, hint)`
   at the transpiler site.
3. Add a `docs/errors/GALA-Exxxx.md` page using the template below.
4. Update the index table above.
5. Add a negative test in `internal/transpiler/transformer/error_codes_test.go`
   that exercises the new code end-to-end.

## Page template

```markdown
# GALA-Exxxx — Short title

**When it fires.** Plain-English description of the invariant that was violated.

**Minimal repro.**
```gala
// paste the smallest code that triggers the error
```

**Error output.** `[SemanticError GALA-Exxxx] file.gala:L:C <message> (hint: <hint>)`

**Fix.** One or two paragraphs describing how to correct the code.

**Rationale.** Why the rule exists — usually the class of bug it prevents or
the semantic it protects. Links to related transpiler work or issues.
```
