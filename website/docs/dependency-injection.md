---
layout: default
title: "Dependency Injection in GALA — Explicit Wiring and Interface Literal Fakes"
description: "How to do dependency injection in GALA without a container or implicit parameters: interfaces as ports, dependencies as constructor parameters with defaults, a composition root, and tests that fake dependencies with interface literals and Copy. Every listing is a tested program."
keywords: "gala dependency injection, golang dependency injection without framework, gala interface literal, gala fake for tests, gala composition root, gala constructor injection, gala mock interface"
permalink: /docs/dependency-injection/
last_modified_at: 2026-10-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Dependency Injection</p>

{% raw %}

# Dependency Injection in GALA

GALA has no injection container, no annotations and no implicit parameters.
A dependency is an ordinary constructor parameter, wired once in one place.
Everything a piece of code uses appears in its signature, and every value it
receives is visible where it is built.

This guide shows the pattern and how to test it. All the listings come from
[`examples/dependency_injection.gala`](https://github.com/martianoff/gala/blob/master/examples/dependency_injection.gala),
which runs as part of the test suite.

## Table of Contents

1. [The pattern](#the-pattern)
2. [Ports: what a service needs](#ports-what-a-service-needs)
3. [Services: dependencies as constructor parameters](#services-dependencies-as-constructor-parameters)
4. [The composition root](#the-composition-root)
5. [Testing with interface literals](#testing-with-interface-literals)
6. [Interface or function?](#interface-or-function)
7. [Dependencies and concurrency](#dependencies-and-concurrency)
8. [Dependencies and Go](#dependencies-and-go)
9. [What GALA does not do](#what-gala-does-not-do)

## The pattern

1. **Ports.** Declare what a service needs as interfaces: a repository, a
   clock, a logger.
2. **Services.** A service is a struct whose fields are its dependencies. An
   optional dependency has a default.
3. **Adapters.** Production implementations are ordinary structs with methods.
4. **Composition root.** `main` builds the adapters and passes them to the
   services. It is the one place that names concrete implementations.
5. **Tests** build the same services with fakes: interface literals, swapped
   in with `Copy`.

## Ports: what a service needs

A port is an interface that says what the service needs, in the service's
own terms:

<!-- doc-source: examples/dependency_injection.gala -->
<!-- doc-check: fragment -->
```gala
type UserRepo interface {
    Find(id int) Option[User]
}

type Clock interface {
    Year() int
}

type Logger interface {
    Info(msg string)
}
```

Keep ports small. A service that needs one query from a repository should not
depend on the repository's twenty methods; a small interface is also a short
fake.

## Services: dependencies as constructor parameters

A service is a shorthand struct. Its field list is its constructor, so a
missing dependency is a compile error
([`GALA-E0045`](/docs/errors/gala-e0045/)), and an optional one takes its default:

<!-- doc-source: examples/dependency_injection.gala -->
<!-- doc-check: fragment -->
```gala
// An optional dependency gets a default.
func NoopLogger() Logger = Logger(Info = (msg) => {})

struct Greeter(repo UserRepo, clock Clock, log Logger = NoopLogger())

func (g Greeter) Greet(id int) string = g.repo.Find(id) match {
    case Some(u) => {
        g.log.Info(s"greeting ${u.Name}")
        s"Hello, ${u.Name} (${g.clock.Year()})"
    }
    case None() => s"no user $id"
}
```

`NoopLogger` builds its value with an [interface literal](/docs/language-reference/#interface-literals):
a value of an interface made from one function per method, without declaring
a struct for it.

Adapters are ordinary structs that implement the ports:

<!-- doc-source: examples/dependency_injection.gala -->
<!-- doc-check: fragment -->
```gala
struct MemRepo(users HashMap[int, User])
func (r MemRepo) Find(id int) Option[User] = r.users.Get(id)

struct FixedClock(year int)
func (c FixedClock) Year() int = c.year
```

## The composition root

`main` wires everything, with named arguments so each dependency is labeled:

<!-- doc-source: examples/dependency_injection.gala -->
<!-- doc-check: fragment -->
```gala
func main() {
    // Composition root: the one place that names concrete implementations.
    val greeter = Greeter(
        repo = MemRepo(users = HashMapOf((1, User(1, "Ada")))),
        clock = FixedClock(2026),
        log = PrefixLogger("[info]"),
    )
    Println(greeter.Greet(1))
    Println(greeter.Greet(2))
```

In a larger application, each layer gets a constructor function that takes
the lower layer's services and returns its own, and `main` calls them in
order. The wiring stays plain code: a reader follows it with "go to
definition", and the compiler checks every step.

## Testing with interface literals

A test builds the service with fakes. An interface literal is a fake in one
line: its lambdas take their parameter types from the interface's methods,
and the compiler checks that every method is given
([`GALA-E0078`](/docs/errors/gala-e0078/)). `Copy` replaces only the dependencies
a test cares about:

<!-- doc-source: examples/dependency_injection.gala -->
<!-- doc-check: fragment -->
```gala
    // In a test: fakes are interface literals, and Copy swaps one dependency.
    val fakeRepo = UserRepo(Find = (id) => Some(User(id, s"user$id")))
    val testGreeter = greeter.Copy(repo = fakeRepo, clock = Clock(Year = () => 2030))
    Println(testGreeter.Greet(7))
```

A fake can record how it was used, because a literal's functions close over
local state:

<!-- doc-source: examples/dependency_injection.gala -->
<!-- doc-check: fragment -->
```gala
    // A recording fake checks how a dependency was used.
    var messages = ListOf[string]()
    val recording = greeter.Copy(log = Logger(Info = (msg) => { messages = messages.Append(msg) }))
    recording.Greet(1)
    Println(s"logged: $messages")
```

With the [test framework](https://github.com/martianoff/gala/blob/master/docs/GALA.MD#15-testing), the same fakes go into a test
function:

```gala
package main

import . "martianoff/gala/test"

struct User(Id int, Name string)

type UserRepo interface {
    Find(id int) Option[User]
}

struct Greeter(repo UserRepo)

func (g Greeter) Greet(id int) string = g.repo.Find(id) match {
    case Some(u) => s"Hello, ${u.Name}"
    case None() => s"no user $id"
}

func TestGreetUnknownUser(t T) T {
    val greeter = Greeter(repo = UserRepo(Find = (id) => None()))
    return Eq(t, greeter.Greet(9), "no user 9")
}
```

No mocking library and no generated mocks: a fake is GALA code, checked like
any other.

## Interface or function?

A dependency with a single operation can be a function-typed field instead of
an interface:

```gala
package main

struct Report(now func() int)

func (r Report) Header() string = s"Report for ${r.now()}"

func main() {
    val report = Report(now = () => 2026)
    Println(report.Header())
}
```

Prefer an interface when the dependency has several related operations, when
it has a production adapter with state of its own, or when Go code
implements it. Prefer a function when it is one operation, such as a clock or
an ID generator. Both are faked the same way: with a lambda.

## Dependencies and concurrency

A dependency that is a function or an interface may hold mutable state: an
interface literal can close over a `var`. The data-race check therefore does
not let a `Future` body capture one
([`GALA-E0037`](/docs/errors/gala-e0037/)). Call the dependency first, and send
the plain value into the `Future`:

```gala
package main

import . "martianoff/gala/concurrent"

type Clock interface {
    Year() int
}

func main() {
    val clock = Clock(Year = () => 2026)
    val year = clock.Year()
    val next = Future(() => year + 1)
    Println(next.Await())
}
```

## Dependencies and Go

- **Go implementations are injected like GALA ones.** A Go type with the
  methods of a GALA interface implements it, so a Go adapter is passed to a
  GALA service as is.
- **Go interfaces are ports too.** A service can depend on `io.Writer` or
  `fmt.Stringer`, and a test fakes it with an interface literal:
  `io.Writer(Write = (p) => Success(p.Size()))`. A method with a Go result
  list, such as `Write`'s `(int, error)`, takes a lambda returning a `Try`.
- **Go callers** see a GALA service as a Go struct whose fields are wrapped in
  `std.Immutable`. To let Go build one, export a GALA constructor function
  that takes the dependencies (see the [Go Interop guide](/docs/go-interop/)).

## What GALA does not do

GALA leaves out the implicit mechanisms other languages use for this:

- **No implicit or context parameters.** Values filled in from the caller's
  scope make a call site hide its inputs, and a rename or a shadowing binding
  silently changes what a call receives.
- **No reflection container.** Wiring resolved at run time turns a missing
  dependency into a startup failure instead of a compile error.

Explicit parameters cost a few more words at the composition root and in
tests. In return every dependency is visible, checked by the compiler, and
easy to replace.

{% endraw %}
