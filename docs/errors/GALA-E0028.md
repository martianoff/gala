# GALA-E0028 — Type alias redeclared

**When it fires.** A type alias (`type Handler func(string) string`) with the
same name is declared more than once in the same package. The transformer keeps
one lookup table of alias name → underlying type; a second alias under the same
name would replace the first.

Like [GALA-E0027](GALA-E0027.md), the alias table is seeded from sibling files
when transformation begins, so this catches both within-file duplicates and
duplicates split across two files of the same package.

**Minimal repro.**

```gala
package main

type Handler func(string) string
type Handler func(int) int

func main() {
    Println("unused")
}
```

**Error output.**

```
error[GALA-E0028]: type alias "Handler" already declared in package "main"
  --> main.gala:4:6
  |
4 | type Handler func(int) int
  |      ^^^^^^^ remove the duplicate declaration or rename one of the aliases
  |
  = hint: remove the duplicate declaration or rename one of the aliases
```

The caret points at the *second* alias's name — the one to rename or remove.

**Fix.** Give each alias a distinct name that says what it aliases:

```gala
package main

type StringHandler func(string) string
type IntHandler func(int) int

func main() {
    val upper StringHandler = (s) => s + "!"
    val double IntHandler = (n) => n * 2
    Println(upper("hi"))
    Println(double(21))
}
```

**Rationale.** The silent overwrite was worse than a lost declaration: because
the alias table is consulted whenever a type name needs resolving, the *second*
alias's underlying type would be substituted at call sites written against the
first. The resulting Go failed to compile at some unrelated line, or — when both
underlying types happened to be structurally compatible — compiled and did the
wrong thing. Rejecting at the declaration keeps the failure local.

**Scope.** Type aliases (`type Foo = Bar` / `type Foo func(...)`). Duplicate
*type declarations* (structs, sealed types, interfaces) are
[GALA-E0011](GALA-E0011.md).

**Related redeclaration codes.** [GALA-E0011](GALA-E0011.md) types · [GALA-E0012](GALA-E0012.md) methods · [GALA-E0027](GALA-E0027.md) functions · [GALA-E0028](GALA-E0028.md) type aliases · [GALA-E0029](GALA-E0029.md) interface method specs · [GALA-E0030](GALA-E0030.md) struct fields · [GALA-E0031](GALA-E0031.md) sealed cases.
