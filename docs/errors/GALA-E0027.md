# GALA-E0027 — Function redeclared

**When it fires.** A top-level function with the same name is declared more than
once in the same package (across any combination of files). GALA mirrors Go's
"redeclared in this block" rule: a package has one namespace for its top-level
functions, and there is no overloading — differing parameter lists do not make
two `greet` functions distinct.

**Minimal repro — two files.**

```gala
// a.gala
package main

func greet(name string) string = "hello " + name
```

```gala
// b.gala
package main

func greet(other string) string = "hi " + other

func main() {
    Println(greet("world"))
}
```

**Error output.**

```
Error transpiling a.gala: [SemanticError GALA-E0027] a.gala:3:5 function "greet" in package "main" redeclared (also declared at b.gala:3) (hint: remove the duplicate declaration or rename one of the functions)
Error transpiling b.gala: [SemanticError GALA-E0027] b.gala:3:5 function "greet" in package "main" redeclared (also declared at a.gala:3) (hint: remove the duplicate declaration or rename one of the functions)
```

Every file of the package is compiled, so both declarations are reported, each
naming the *other* one. Batch analysis order is not source order, so the message
does not claim either declaration came first — it points at the other
declaration site and leaves the choice of which to delete to you.

**Minimal repro — one file.**

```gala
// a.gala
package main

func greet(name string) string = "hello " + name

func greet(other string) string = "hi " + other

func main() {
    Println(greet("world"))
}
```

**Error output.**

```
Error transpiling a.gala: [SemanticError GALA-E0027] a.gala:5:0 function "greet" in package "main" redeclared (also declared at line 3) (hint: remove the duplicate declaration or rename one of the functions)
```

When the other declaration is in the file the error is reported against, the
message gives its line instead of repeating the file name.

`gala build` reports the same diagnostic in the rich framed CLI form, and stops
at the first one rather than listing both:

```
error[GALA-E0027]: function "greet" in package "main" redeclared (also declared at line 3)
  --> a.gala:5:1
  |
5 | func greet(other string) string = "hi " + other
  | ^^^^ remove the duplicate declaration or rename one of the functi…
  |
  = hint: remove the duplicate declaration or rename one of the functions
```

The annotation beside the caret is the hint truncated to fit; the `= hint:`
footer carries it in full.

**Fix.** Delete the redundant declaration, or rename one of them so each name
describes what it does:

```gala
package main

func greet(name string) string = "hello " + name
func greetInformally(other string) string = "hi " + other

func main() {
    Println(greet("world"))
    Println(greetInformally("world"))
}
```

If you reached for a second definition because you wanted to accept different
argument shapes, GALA's answer is not overloading but **default parameters** or
a **sealed type** matched inside one function:

```gala
package main

func greet(name string, formal bool = true) string =
    if (formal) s"hello $name" else s"hi $name"

func main() {
    Println(greet("world"))
    Println(greet("world", false))
}
```

**Rationale.** The analyzer keys top-level functions by name in a single
per-package map; a second declaration silently overwrote the first. The later
definition won, the earlier was lost, and call sites that expected the first got
the second's body — a whole function disappearing with no diagnostic. Because
the map is populated across the whole package, the same silent overwrite applied
whether the duplicate sat in one file or in two. Rejecting it at the analyzer
names both sites in GALA source terms instead of leaving it to the Go compiler's
`greet redeclared in this block` against generated code.

**Scope.** Top-level functions anywhere in one package. Methods on a type are
covered by [GALA-E0012](GALA-E0012.md); interface method specs by
[GALA-E0029](GALA-E0029.md).

**Related redeclaration codes.** [GALA-E0011](GALA-E0011.md) types · [GALA-E0012](GALA-E0012.md) methods · [GALA-E0027](GALA-E0027.md) functions · [GALA-E0028](GALA-E0028.md) type aliases · [GALA-E0029](GALA-E0029.md) interface method specs · [GALA-E0030](GALA-E0030.md) struct fields · [GALA-E0031](GALA-E0031.md) sealed cases.
