# GALA-E0025 — Unresolved cross-package symbol

**When it fires.** A type or function name was used without a package
qualifier (e.g. bare `Array`, `ArrayTabulate`, `MyType`) and resolves
to a symbol in a GALA package that this file does not import. GALA
mirrors Go's rule: cross-package symbols require an explicit `import`
declaration in the file that uses them. Sibling files' imports do not
propagate.

**Error output.**

```
[SemanticError GALA-E0025] line 6:9 undefined: ArrayTabulate (used in effects.BuildLabels) — 'collection_immutable' is not imported in this file (hint: add an explicit import to this file. For unqualified usage: `import . "<path-ending-in-collection_immutable>"`. For qualified usage: `import "<path>"` and call it as `collection_immutable.ArrayTabulate`. Sibling files' imports do not propagate.)
```

**Fix.** Add the explicit import. Two equivalent forms:

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

**Rationale.** Up to GALA 0.39 the analyzer fell back to "current
package qualification" when a bare name didn't resolve. That silently
mis-qualified cross-package types as belonging to the current package
and broke type-param inference downstream — `func(i int) any` instead
of `func(i int) string` was the canonical failure. Promoting this to
a coded error matches Go's compile-time safety: every cross-package
reference is enforced at the import declaration, not at call-site
type inference.

**Scope.** Analyzer post-pass (validateExplicitImports). The
transformer no longer relies on the fallback because every name
reaching it is either qualified or guaranteed to have a matching
import.
