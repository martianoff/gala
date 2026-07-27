# GALA-E0025 — Unresolved cross-package symbol

**When it fires.** A type or function name was used without a package
qualifier (e.g. bare `Array`, `ArrayTabulate`, `MyType`) and resolves
to a symbol in a GALA package that *this file* does not import. GALA
mirrors Go's rule: cross-package symbols require an explicit `import`
declaration in the file that uses them. Sibling files' imports do not
propagate — which is exactly how this usually sneaks in, so the repro
below needs two files.

**Minimal repro.**

`effects/seed.gala` — imports the package:

```gala
package effects

import . "martianoff/gala/collection_immutable"

func Seed() Array[string] = ArrayOf("a", "b")
```

`effects/labels.gala` — same package, *no* import of its own:

```gala
package effects

func BuildLabels(n int) Array[string] = ArrayTabulate(n, (i) => s"row=$i")
```

**Error output.** The check walks a declaration's types, so the first
offender reported is the **return type** `Array` — not `ArrayTabulate` in the
body. The caret is on the declared function name, and the context in
parentheses is package-qualified (`effects.BuildLabels`).

```text
error[GALA-E0025]: undefined: Array (used in effects.BuildLabels) — 'collection_immutable' is not imported in this file
  --> effects/labels.gala:3:6
  |
3 | func BuildLabels(n int) Array[string] = ArrayTabulate(n, (i) => s"row=$i")
  |      ^^^^^^^^^^^ add an explicit import to this file
  |
  = hint: add an explicit import to this file. For unqualified usage: `import . "<path-ending-in-collection_immutable>"`. For qualified usage: `import "<path>"` and call it as `collection_immutable.Array`. Sibling files' imports do not propagate.
```

The `-->` line echoes the source path as the compiler resolved it; the CLI
prints it absolute. Note the hint's qualified-usage example names the symbol
actually being reported (`collection_immutable.Array`).

**The `used in ...` context is not always qualified.** It is the analyzer's
metadata key for the offending declaration, and that key carries a package
prefix for every package *except* `main` and `test`. Move the same two files
into `package main` and the identical failure reads:

```text
error[GALA-E0025]: undefined: Array (used in BuildLabels) — 'collection_immutable' is not imported in this file
```

Everything else — the caret, the locus, the hint — is unchanged; only the
parenthesized context loses its prefix. Search for the symbol name rather than
the whole parenthesized phrase.

**Fix.** Add the explicit import to the offending file. Two equivalent forms:

```gala
package effects

// dot import — the symbols come into scope unqualified
import . "martianoff/gala/collection_immutable"

func BuildLabels(n int) Array[string] = ArrayTabulate(n, (i) => s"row=$i")
```

```gala
package effects

import "martianoff/gala/collection_immutable"

func BuildLabels(n int) collection_immutable.Array[string] =
    collection_immutable.ArrayTabulate(n, (i) => s"row=$i")
```

**Rationale.** The analyzer used to fall back to "current package
qualification" when a bare name didn't resolve. That silently
mis-qualified cross-package types as belonging to the current package
and broke type-param inference downstream — `func(i int) any` instead
of `func(i int) string` was the canonical failure. Promoting this to
a coded error matches Go's compile-time safety: every cross-package
reference is enforced at the import declaration, not at call-site
type inference.

**Scope.** Analyzer post-pass (`validateExplicitImports`). The
transformer no longer relies on the fallback because every name
reaching it is either qualified or guaranteed to have a matching
import.
