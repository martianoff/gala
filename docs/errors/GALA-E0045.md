# GALA-E0045 — missing required field in struct construction

**When it fires.** A shorthand struct was constructed with call syntax, and the
call omitted a field that declares no default:

```gala
struct Cfg(Name string, Tries int)

val c = Cfg(Name = "a")   // Tries has no default and was not passed
```

Both call forms are covered — named (`Cfg(Name = "a")`) and positional
(`Cfg("a")`) — because both are constructor calls.

**Minimal repro.**

```gala
package main

struct Cfg(Name string, Tries int)

func main() {
    val c = Cfg(Name = "a")
    Println(c.Tries)
}
```

**Error output.**

```
error[GALA-E0045]: missing required field "Tries" in construction of "Cfg"
  --> main.gala:6:17
  |
6 |     val c = Cfg(Name = "a")
  |                 ^^^^ pass "Tries", or give the field a default in the declaration
  |
  = hint: pass "Tries", or give the field a default in the declaration (e.g. Tries int = 0)
```

Every omitted field is named at once, so a call missing several takes one round
trip rather than several.

**Fix.** Either pass the field:

```gala
val c = Cfg(Name = "a", Tries = 3)
```

or declare a default, which makes it optional at every call site:

```gala
struct Cfg(Name string, Tries int = 3)

val c = Cfg(Name = "a")   // Tries = 3
```

**Rationale.** A shorthand struct's field list reuses the grammar's `parameter`
rule — it *is* a constructor signature, and `= value` on a field means what it
means on a function parameter. Before this check, neither half of that held:

```gala
struct Cfg(Name string, Mask rune = '•', Tries int = 3)

val a = Cfg(Name = "a")
// Mask = 0, Tries = 0
```

The default was parsed and discarded, so the field took Go's zero value. A
defaulted `rune` became NUL and a defaulted `int` became 0, with no diagnostic —
and the reason to give a new field a default is precisely so existing call sites
keep compiling, which is exactly when the silent zero reaches them. A password
mask declared `'•'` rendered as a run of NUL characters: a field that looks
empty while holding a secret.

Omitting a field with *no* default was equally silent, so the `= value` syntax
was not gating omission either — it neither supplied the value nor made the
field optional, because the field was already optional. And since a zero-valued
`rune`, `int` or `bool` is often a legal value, nothing downstream could tell
"the caller omitted it" from "the caller meant 0". The workaround was to make
the zero value mean "default" by hand, which is the boilerplate the default
existed to remove.

**Where it stands down.** This code fires for **shorthand-declared GALA structs
constructed with call syntax**, and nowhere else. Everything that is or mirrors a
Go struct keeps Go's partial-literal semantics, because Go's zero-value contract
is what the Go boundary runs on.

- **Go-imported types are exempt, whichever syntax names them.** Both
  `url.URL{Scheme: "x"}` and `url.URL(Scheme = "x")` construct partially and
  raise nothing. `url.URL` has ten fields and callers set two; `http.Server` and
  `tls.Config` are the same shape. Requiring every field would make the Go
  standard library unconstructable, so for a Go type the call form is sugar for
  the literal rather than a GALA constructor.
- **Go-style literals keep Go's semantics.** `Cfg{Name: "a"}` is a composite
  literal, not a constructor call — the braces are Go's spelling and behave like
  Go's. It stays partial, never consults defaults, and never raises this error.
  When a struct has no defaults and a call omits every field, the hint points at
  this form.
- **Block-form structs are exempt.** `type Cfg struct { ... }` mirrors a Go
  declaration field for field, which is what makes it the right choice for a
  type crossing into Go — and it has no syntax for a field default, so requiring
  every field would leave no way to opt out. The standard library relies on
  this: a HAMT node in `collection_immutable` sets two of its four fields and
  means the rest to be zero.
- **`Copy` is unaffected.** `c.Copy(Name = "b")` carries every field forward
  from the receiver, so an omitted field keeps its current value rather than
  being re-defaulted or reported.

The shorthand form is the one place GALA declares a *constructor* rather than a
*layout*, so it is the one place the language can promise a fully-initialized
value — which is why it is the one place this check applies.

**Evaluation.** A field default is re-evaluated at each construction, not once
at declaration — the same contract function parameter defaults have. A field
declared `At time.Time = time.Now()` records the time of each construction.

**Scope.** This code covers an omitted field in shorthand struct construction.
A missing argument to an ordinary function or method is reported separately, and
a type name called where it constructs nothing is
[GALA-E0043](GALA-E0043.md).
