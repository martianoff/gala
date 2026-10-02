# GALA-E0019 — Empty parenthesized expression

**When it fires.** The source contains an empty parenthesized expression,
`()`, in a position where an expression is required (e.g. as a match-arm
body, an argument, or the right-hand side of a declaration). The grammar
admits the form because the inner `expressionList` is optional, but GALA
has no unit/void literal the transpiler can lower to a Go expression — so
attempting to emit `()` would produce a nil AST node that crashes Go's
printer downstream.

The most common offender is a void-arm shorthand:

```gala
func handle(code int) {
    code match {
        case 0 => Println("zero")
        case _ => ()                  // ← GALA-E0019: not allowed
    }
}
```

**Error output.**

```
[SemanticError GALA-E0019] file.gala:6:18 empty parenthesized expression "()" cannot be used as a value (hint: use a real statement (e.g. `Println("…")`) or remove the arm if it cannot occur)
```

**Fix.** Replace `()` with whatever the arm actually does:

<!-- doc-check: fragment -->
```gala
case _ => Println("other")        // an actual side-effecting call
case _ => { /* no-op block */ }   // an explicit empty block, when allowed
```

If the arm represents an unreachable case, fail loudly with a coded message so
a future bug surfaces. A bare `panic(...)` is [GALA-E0035](GALA-E0035.md);
GALA's form is `go_builtins.Panic` (import `"martianoff/gala/go_builtins"`):

<!-- doc-check: fragment -->
```gala
case _ => go_builtins.Panic("unreachable: handler received unexpected code")
```

**Rationale.** GALA's grammar reuses `expressionList?` inside `'(' … ')'`
so that paired and 1-arity cases share a rule. The nil-list branch was
previously unhandled in the transformer; before this code existed, the
emit path produced a nil `ast.Expr` that crashed Go's printer with a
generic `ast.Walk: unexpected node type <nil>` (now wrapped as
`GALA-E0017`). Surfacing the failure at its real site — the GALA source —
gives users an actionable hint instead of a transpiler stack trace.

**Scope.** Only the literal expression `()`. Calls written as `f()` are
unaffected (the empty argument list is a different grammar production).
A future language change that introduces an explicit unit value would
remove this error.
