# GALA-E0005 — Missing `Unapply` on extractor

**When it fires.** A pattern invokes something as an extractor (e.g.,
`case Foo(x) =>`) but the referenced type has no `Unapply` method. GALA
patterns use Scala-style extractor semantics: any type used as a pattern
must define `Unapply` (returning either `bool` for guard patterns or
`Option[T]` / `Option[TupleN[...]]` for field extraction).

**Minimal repros.**

```gala
// Typo: Som instead of Some
val r = Some(42) match {
    case Som(x) => x
    case _      => 0
}

// Type exists but has no Unapply
type Bucket struct { items Array[int] }
val r = bucket match {
    case Bucket(xs) => xs   // Bucket is a struct, not an extractor
    case _          => ArrayOf[int]()
}
```

**Error output.**

```
[SemanticError GALA-E0005] main.gala:3:10 extractor 'Som' must define an Unapply method. ... (hint: did you mean 'Some'?)
```

The transpiler suggests near matches from the companion-object registry,
so typos usually come back with a "did you mean?" hint.

**Fix.**

- **Typos**: use the name the hint suggests.
- **Missing extractor**: define one. For a guard extractor on a concrete
  type:
  ```gala
  type Even struct {}
  func (e Even) Unapply(i int) bool = i % 2 == 0
  ```
  For a generic extractor that pulls out values:
  ```gala
  type Wrap[T any] struct {}
  func (w Wrap[T]) Unapply(container Container[T]) Option[T] = ...
  ```
- **Struct matching**: if `Bucket` is meant to be pattern-matched by field,
  every public-uppercase field auto-generates an Unapply — the issue there
  is usually a name mismatch or a private field.

**Rationale.** Without the `Unapply` contract, any capitalized identifier
in a pattern would be silently treated as a variable binding, meaning
`case Som(x) =>` would compile as "bind the matched value to `Som`" and
discard the inner `(x)`. The explicit error with a "did you mean?"
suggestion catches the typo cleanly, and the required `Unapply` shape
keeps extractors self-documenting.

**Related work.** Error added in B7 (PR #166). Suggestions powered by
`suggestExtractorName` + `levenshteinDistance` in `utils.go`.
