# GALA-E0071 — Invalid alternative pattern

**When it fires.** An alternative pattern `p1 | p2` matches when any of its
alternatives matches ([Alternative Patterns](../GALA.MD#alternative-patterns)).
The error fires when one is malformed:

- an alternative **binds a name**, as `Some(n)` does in `case Some(n) | None()`;
- `|` shares an operand chain with `+`, `-` or `^` without parentheses, as in
  `case 1 + 1 | 3`. Those operators have the same precedence as `|`, so it is
  not clear which values are the alternatives.
- `|` sits inside a comparison or boolean expression of the pattern, as in
  `case Some(1 | 2 > 1)`;
- an alternative is the wildcard `_`, as in `case Some(1) | _`: it matches
  everything, so the other alternatives never decide. Write `case _` instead.

**Minimal repro.**

```gala
package main

func describe(o Option[int]) string = o match {
    case Some(n) | None() => "value"
}

func main() {
    Println(describe(Some(1)))
}
```

**Error output.**

```
error[GALA-E0071]: pattern alternative binds 'n'; an alternative pattern cannot bind names
  --> main.gala:4:10
  |
4 |     case Some(n) | None() => "value"
  |          ^^^^ use `_` in place of the name, or give each alternative its o…
  |
  = hint: use `_` in place of the name, or give each alternative its own case
```

With `+`:

```gala
package main

func describe(n int) string = n match {
    case 1 + 1 | 3 => "two or three"
    case _ => "other"
}

func main() {
    Println(describe(2))
}
```

```
error[GALA-E0071]: '|' separates pattern alternatives and cannot be mixed with '+' in a pattern
  --> main.gala:4:10
  |
4 |     case 1 + 1 | 3 => "two or three"
  |          ^ put parentheses around the alternative that uses '+'
  |
  = hint: put parentheses around the alternative that uses '+'
```

**Fix.** When the arm does not need the value, write `_` instead of the name.
When it does, give each alternative its own case:

```gala
package main

func describe(o Option[int]) string = o match {
    case Some(_) | None() => "value"
}

func label(o Option[int]) string = o match {
    case Some(n) => s"some $n"
    case None() => "none"
}

func main() {
    Println(describe(Some(1)), label(Some(1)))
}
```

Put parentheses around an alternative that is itself an arithmetic expression:

```gala
package main

func describe(n int) string = n match {
    case (1 + 1) | 3 => "two or three"
    case _ => "other"
}

func main() {
    Println(describe(2))
}
```

**Rationale.** Every alternative has to bind the same names with the same
types, or the arm's body would read a name that one alternative never set.
Scala has the same rule. Without alternatives, `|` in a pattern meant Go's bitwise OR:
`case 1 | 2` compared the subject with `3`. To match a bitwise-OR value, use a
guard, `case n if n == (FlagA | FlagB)`, or name it with a `val` and match the
name.
