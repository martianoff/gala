# GALA-E0089 — Case guard is not a bool

**When it fires.** The guard of a case clause, `case p if guard`, has a type
other than `bool`. It applies to the arms of a `match` and to the cases of a
partial-function literal (`{ case ... }`). A guard is a condition: the arm is
taken only when the pattern matches and the guard is true.

**Minimal repro.**

```gala
package main

func main() {
    val pf func(int) Option[int] = { case n if n => n }
    Println(pf(1))
}
```

**Error output.**

```
error[GALA-E0089]: case guard `n` has type int, not bool
  --> main.gala:4:48
  |
4 |     val pf func(int) Option[int] = { case n if n => n }
  |                                                ^ a guard must be a bool condition, such as a comparison
  |
  = hint: a guard must be a bool condition, such as a comparison
```

**Fix.** Write the condition the guard stands for, such as a comparison:

```gala
package main

func main() {
    val pf func(int) Option[int] = { case n if n > 0 => n }
    Println(pf(1))
}
```

**Rationale.** GALA has no truthiness: an `int`, a `string` or an `Option`
is not true or false. The guard used to be joined to the pattern's test with
`&&` unchecked, so `go build` rejected the generated condition
("mismatched types untyped bool and int") instead of GALA reporting the guard.
