---
layout: default
title: "GALA-E0044 — Type Has No Such Method"
description: "\"Array has no method Sum\" — GALA-E0044 catches a method a GALA type does not declare and suggests the nearest real one, instead of letting go build describe the generated expression."
keywords: "gala-e0044, gala unknown method, gala no such method, gala did you mean, gala method not found, gala array methods, gala foldleft"
permalink: /docs/errors/gala-e0044/
last_modified_at: 2026-08-30
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0044</p>

# GALA-E0044 — type has no such method

**When it fires.** A method was called on a value whose GALA type declares no
method of that name:

```gala
val xs = ArrayOf(1, 2, 3)
Println(xs.Sum())
```

`Array` has no `Sum`. The diagnostic names the nearest real method when the
call looks like a typo, and otherwise lists methods the type does have.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(xs.Sise())
}
```

**Error output.**

```
error[GALA-E0044]: Array has no method Sise
  --> main.gala:7:16
  |
7 |     Println(xs.Sise())
  |                ^^^^ did you mean `Size`?
  |
  = hint: did you mean `Size`?
```

When no method is close enough to suggest, the hint lists the surface instead:

```
error[GALA-E0044]: Array has no method Sum
  --> main.gala:7:16
  |
7 |     Println(xs.Sum())
  |                ^^^ Array declares: Append, Contains, Drop, Exists, ...
```

**Fix.** Call a method that exists. For the `Sum` case, GALA folds:

```gala
val total = xs.FoldLeft(0, (acc, x) => acc + x)
```

**Rationale.** The rejection is not new — this call never compiled. What was
new is *who* reported it and *in whose words*. The call used to be emitted
verbatim and handed to `go build`, which described the **generated** expression
rather than the source:

```
xs.Get().Filter(func(x int) bool {…}).Sum undefined
  (type collection_immutable.Array[int] has no field or method Sum)
```

The `.Get()` in that message is the `Immutable[T]` auto-unwrap the transpiler
inserts. The user wrote `xs.Filter(...).Sum()` and got back an expression
containing a call they never made, described in terms of a Go type they never
named. Nothing in it suggested `FoldLeft`.

**Where it stands down.** A false positive here rejects a correct program, so
the check only fires when the judgement is safe. It says nothing when:

- the receiver's type is not a GALA type — every Go-interop receiver;
- the receiver's type parameters are not all bound to concrete arguments, so a
  receiver still mid-inference is never judged;
- the method is one the transformer *generates* rather than the user declaring
  it — `Copy`, `Equal`, `Apply`, `Unapply`, `String`, the `is<Variant>`
  predicates, and the codec pair. None of these appear in the type's declared
  method set, so a check that consulted only that set would reject all of them;
- the name matches a **field** holding a function, which is called exactly like
  a method;
- the type declares no methods at all *and* comes from another package, where
  an empty method set may mean the metadata was loaded without it rather than
  that the type genuinely has none. A type declared in the package being
  compiled is fully known and is still checked.

**Scope.** This code covers a method call on a known, concrete GALA type. It
does not cover an unknown *function* ([GALA-E0023](/docs/errors/gala-e0023/)), a type name
called as a constructor ([GALA-E0043](/docs/errors/gala-e0043/)), or a call with the wrong
number of arguments to a method that does exist.
