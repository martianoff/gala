---
layout: default
title: "GALA-E0109 — A Type Pattern Tests for an Interface a Type Has Only Through Free Functions"
description: "GALA-E0109 fires when a type pattern tests for an interface whose methods a type has only as generated free functions, so the arm could never match."
keywords: "gala-e0109, gala type pattern, gala interface, gala free function, gala generic method, gala match"
permalink: /docs/errors/gala-e0109/
last_modified_at: 2026-11-13
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0109</p>

# GALA-E0109 — A type pattern tests for an interface a type has only through free functions

**When it fires.** A type pattern `case x: I` tests for an interface `I`, and
some type has every method of `I`, but at least one of them is generated as a
free function rather than a Go method. A method of a generic type whose
signature names another generic type, such as `HashMap.Keys()` or
`Array.ToList()`, is generated that way unless an interface that the type's
own package can see needs it (see
[Methods of generic types that Go sees as functions](/docs/go-interop/#methods-of-generic-types-that-go-sees-as-functions)).
Go's type test never matches a value of that type, so the arm would quietly
never be taken.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

type HasKeys interface {
    Keys() HashSet[string]
}

func probe(x any) string = x match {
    case h: HasKeys => "keys"
    case _ => "none"
}

func main() = Println(probe(HashMapOf(("a", 1))))
```

**Error output.**

```
error[GALA-E0109]: a value of HashMap never matches `HasKeys`: its Keys is generated as a free function (it names another generic type), so the type does not have it in Go
  --> main.gala:10:10
   |
10 |     case h: HasKeys => "keys"
   |          ^^^^^^^^^^ match the concrete type
   |
   = hint: match the concrete type (`case x: HashMap`) and call Keys on it, or declare HasKeys where HashMap's package sees it so the method stays a method
```

**Fix.** Match the concrete type and call the method on it:

```gala
package main

import . "martianoff/gala/collection_immutable"

func probe(x any) string = x match {
    case m: HashMap[string, int] => s"keys ${m.Keys()}"
    case _ => "none"
}

func main() = Println(probe(HashMapOf(("a", 1))))
```

For a type of your own, declare the interface in the type's package, or in a
package that package imports. The method then stays a method, and both the
type pattern and the static use work.

**Rationale.** Go compiles every method of a generic type once per type
argument, whether or not anything calls it. A method that names another
generic type would make every instantiation compile that type's methods too,
so it becomes a free function that is compiled only where it is called. A
call `m.Keys()` works the same either way. Only Go's method set changes, and
with it which interfaces the type implements. Using the value as such an
interface statically is [GALA-E0100](/docs/errors/gala-e0100/).

---

## Related

- [Go interop: methods of generic types that Go sees as functions](/docs/go-interop/#methods-of-generic-types-that-go-sees-as-functions)
- [All GALA error codes](/docs/errors/)
