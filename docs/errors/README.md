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
