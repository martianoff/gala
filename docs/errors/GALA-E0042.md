# GALA-E0042 — lambda parameter is not parenthesized

**When it fires.** A lambda was written with a bare parameter name instead of a
parameter list:

```gala
xs.Map(x => x * 2)
```

GALA's lambda syntax is `parameters '=>' body`, and `parameters` is always a
parenthesized list — even when there is exactly one parameter, and even when its
type is inferred.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(xs.Map(x => x * 2))
}
```

**Error output.**

```
error[GALA-E0042]: lambda parameters must be parenthesized
  --> main.gala:7:20
  |
7 |     Println(xs.Map(x => x * 2))
  |                    ^ use `(x) => ...`
  |
  = hint: use `(x) => ...`; GALA always parenthesizes a lambda's parameter list, including a single parameter
```

**Fix.** Add the parentheses:

```gala
xs.Map((x) => x * 2)
```

The same applies wherever a lambda is written, not only in an argument:

```gala
val double = (x) => x * 2          // not: val double = x => x * 2
val add    = (a, b) => a + b       // already a list, already correct
```

Zero-parameter lambdas keep the empty list, and a lambda stored in a `val`
without a target type still annotates its parameters:

```gala
Try(() => risky())
val f = (x int) => x * 2
```

**Rationale.** Scala, Kotlin, JavaScript and C# all accept a bare single
parameter, so the form is a common reflex. GALA does not accept it, and the
grammar is deliberately not relaxed to do so.

The reason is `case`. A match arm is `'case' pattern '=>' body`, and `pattern`
reaches `expression`, which reaches `primaryExpr`, whose **first** alternative
is `lambdaExpression`. Admitting a bare-identifier lambda would therefore make

```gala
case x => body
```

ambiguous: `x => body` matches the new lambda form, leaving the arm's own `=>`
missing. ANTLR resolves that kind of conflict by prediction rather than by
erroring, so the greedy reading would win silently and the arm would quietly
mean something other than what it says. A mis-parse that still compiles is a
worse failure than a syntax error, so the single spelling is kept and this
diagnostic explains it.

Admitting the bare form in argument position only — where `case` cannot reach —
was considered and rejected: it would not help `val f = x => …`, which travels
the same `expression` path as a pattern, so the diagnostic would still be needed
and the language would have gained a rule that holds in one position and not
another.

**What it replaces.** The rejection was always correct; the message was not. A
bare parameter derails the parser for the rest of the expression, and ANTLR
re-reported it against each enclosing rule it unwound through:

```
error: no viable alternative at input '(xs.Map(x=>'
error: no viable alternative at input '(x=>'
error: extraneous input '=>' expecting {'{', '}', '(', '[', ';', '+', '-', '^', '*', '&', 'map', 'true', 'false', 'nil', '!', '<-', 'val', 'var', 'bind', 'also', 'use', 'func', 'type', 'if', 'for', 'return', 'import', ...}
error: no viable alternative at input '*'
```

Four errors for one missing pair of parentheses, none of them naming
parentheses, and the last with no source position at all. This code reports the
cause once, at the parameter that has to change.

**Scope.** This code fires when the token before an offending `=>` is a plain
identifier. It does not cover a malformed parameter *list* (`(x, => …`), which
remains an ordinary syntax error, and it does not affect `case` arms, which
parse cleanly and never reach it.
