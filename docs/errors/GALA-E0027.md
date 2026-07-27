# GALA-E0027 — Function redeclared

**When it fires.** Two top-level functions with the same name are declared in
the same package. GALA mirrors Go's "redeclared in this block" rule: a package
has one namespace for its top-level functions, and there is no overloading —
differing parameter lists do not make two `greet` functions distinct.

In practice this code fires for duplicates **within a single file**.
Re-analysing the *same* file is not a redeclaration, so incremental builds and
LSP passes do not trip it. Two `greet` declarations split across **sibling files
of the same package** are not reported here today — they fall through to the Go
compiler instead (see *Scope*).

**Minimal repro.**

```gala
package main

func greet(name string) string = "hello " + name
func greet(other string) string = "hi " + other

func main() {
    Println(greet("world"))
}
```

**Error output.**

```
error[GALA-E0027]: function "greet" in package "main" redeclared (first defined in main.gala)
  --> main.gala:4:1
  |
4 | func greet(other string) string = "hi " + other
  | ^^^^ remove the duplicate declaration or rename one of the functions
  |
  = hint: remove the duplicate declaration or rename one of the functions
```

The message names the file the *first* declaration came from and the caret marks
the second.

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
    if (formal) "hello " + name else "hi " + name

func main() {
    Println(greet("world"))
    Println(greet("world", false))
}
```

**Rationale.** The analyzer previously overwrote the earlier metadata entry with
the later one. The second declaration silently won, the first was lost, and call
sites that expected the first got the second's body — a whole function
disappearing with no diagnostic. Rejecting the redeclaration surfaces the
conflict at the declaration site.

**Scope.** Top-level functions declared in the same file. Methods on a type are
covered by [GALA-E0012](GALA-E0012.md); interface method specs by
[GALA-E0029](GALA-E0029.md).

Duplicates across **sibling files** of one package still fail the build, but
from the Go compiler rather than from this code:

```
main.gala:3: greet redeclared in this block
	a.gala:3[...gen/a.gen.go:6:6]: other declaration of greet
```

That message is attributed to the right `.gala` lines via `//line` directives,
but it is phrased in Go's terms and cites a generated-file location alongside
each source location. Compare [GALA-E0028](GALA-E0028.md), which *does* report
cross-file duplicates at the GALA level.
