# GALA-E0023 — Undefined variable

**When it fires.** The inference engine encountered a name reference
that has no binding in the current type environment. Common causes:

- The name is misspelled.
- The name is defined in another package that is not imported.
- The name is bound by a pattern that did not actually match (e.g.
  shadowed by an outer binding or referenced outside its arm).

**Error output.**

```
[SemanticError GALA-E0023] line 0:0 undefined variable: foo (hint: check the spelling, ensure the import that introduces it is in scope, and verify the binding is reachable from the reference site)
```

**Fix.** Import the symbol, fix the typo, or move the reference
inside the scope where it is bound. If the name is supposed to come
from a dot-imported package, confirm the import has not been silently
dropped (look for warnings about unused dot imports).

**Rationale.** Undefined-variable errors are the single most common
category of inference failure during early development; promoting
them to a stable code lets editors and CI tools attach extra context
(e.g. "did you mean…" suggestions sourced from the type environment).

**Scope.** Inference engine only. The transformer's own scope-walker
emits its own diagnostics for undefined identifiers in non-inference
contexts.
