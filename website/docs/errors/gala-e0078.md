---
layout: default
title: "GALA-E0078 — Invalid Interface Literal"
description: "GALA-E0078 fires when an interface literal Repo(Find = ...) leaves out a method, names one the interface does not have, gives one twice or positionally, or targets a method a function cannot implement."
keywords: "gala-e0078, gala interface literal, gala anonymous interface implementation, gala implement interface with lambdas, gala fake interface test"
permalink: /docs/errors/gala-e0078/
last_modified_at: 2026-10-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0078</p>

# GALA-E0078 — Invalid interface literal

**When it fires.** An interface literal, `Repo(Find = (id) => ...)`, is
invalid:

- an argument is positional, or names no method of the interface;
- a method is given twice, or left out: a literal gives every method of the
  interface exactly once;
- the interface is generic, and its type arguments are neither written
  (`Box[int](...)`) nor taken from an expected type; or
- the interface has a method a function cannot implement: a generic method,
  or an unexported method of an interface from another package, which only
  that package can implement.

**Minimal repro.**

```gala
package main

type Clock interface {
    Now() int
    Zone() string
}

func main() {
    val clock = Clock(Now = () => 12)
    Println(clock.Now())
}
```

**Error output.**

```
error[GALA-E0078]: interface literal of Clock leaves out method Zone
  --> main.gala:9:17
  |
9 |     val clock = Clock(Now = () => 12)
  |                 ^^^^^ give every method of Clock, as in `Zone = ...`
  |
  = hint: give every method of Clock, as in `Zone = ...`
```

**Fix.** Give every method, each by its name:

```gala
package main

type Clock interface {
    Now() int
    Zone() string
}

func main() {
    val clock = Clock(Now = () => 12, Zone = () => "UTC")
    Println(clock.Now(), clock.Zone())
}
```

For an interface a literal cannot build, declare a struct that implements it.

**Rationale.** A literal is a complete implementation of the interface: the
compiler checks that it gives every method, so a value of the interface never
has a method that does nothing. Naming each method keeps a literal readable
and independent of the order the interface declares its methods in. See
[Interface Literals](/docs/language-reference/#interface-literals).
