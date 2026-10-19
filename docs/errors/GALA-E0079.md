# GALA-E0079 — Invalid sealed-view match

**When it fires.** A type with a method `MatchView() S`, where `S` is a sealed
type, can be matched with `S`'s variants: when a case names one, the match
reads its subject through `MatchView()` and matches the view. Every case of
that match must then apply to the view. The error fires for a case that does
not:

- an extractor, struct pattern or literal of another type — the subject's own
  type included;
- a typed pattern (`x: T`) whose type is not `S`; or
- a binding of the whole subject (`case other =>`), which would bind the view,
  not the subject.

The same holds for a partial function `{ case ... }` whose argument has a view,
and for a sub-pattern that reads its value's view: each alternative of
`Some(None | Slot(_))` must be a variant of the view or `_`.

**Minimal repro.**

```gala
package main

struct Slot(raw int)

func (s Slot) MatchView() Option[int] = When(s.raw >= 0, s.raw)

func describe(s Slot) string = s match {
    case Some(n) => s"slot $n"
    case other   => "empty"
}

func main() {
    Println(describe(Slot(3)))
}
```

**Error output.**

```
error[GALA-E0079]: `case other` would bind the Option[int] view, not the Slot itself; this match reads Slot through MatchView() as Option[int]
  --> main.gala:9:10
  |
9 |     case other   => "empty"
  |          ^^^^^ match only Option[int]'s variants and `_` here
  |
  = hint: match only Option[int]'s variants and `_` here; match the Slot itself in a separate match
```

**Fix.** Match the remaining variant, or use `_` for the rest:

```gala
package main

struct Slot(raw int)

func (s Slot) MatchView() Option[int] = When(s.raw >= 0, s.raw)

func describe(s Slot) string = s match {
    case Some(n) => s"slot $n"
    case None()  => "empty"
}

func main() {
    Println(describe(Slot(3)))
}
```

Inside a case, the subject is still in scope by its own name (`s` above). To
test the subject with its own patterns, write a separate match without the
view's variants.

**Rationale.** A match over a view is a match over `S`: its cases, binding
types and exhaustiveness are those of `S`. A case that only applies to the
subject's own type would make one match test two different values, and a
binding of the whole subject would silently hold the view instead of the
value written before `match`. See
[Sealed Views](../GALA.MD#sealed-views).
