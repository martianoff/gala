# GALA-E0073 — Invalid named sub-pattern

**When it fires.** A named sub-pattern `Field = p` matches `p` against the field
of that name ([Named Sub-Patterns](../GALA.MD#named-sub-patterns)). The error
fires when one is malformed:

- the field does not exist, as `Depth` below;
- a field is matched twice, by name twice or by position and by name;
- a positional sub-pattern follows a named one;
- there are more positional sub-patterns than fields;
- what the pattern matches has no fields to name: an extractor with its own
  `Unapply`. (An opaque-type pattern takes one sub-pattern and reports a named
  one itself.)

**Minimal repro.**

```gala
package main

sealed type Shape {
    case Circle(Radius int)
    case Rect(Width int, Height int)
}

func height(s Shape) int = s match {
    case Circle(Radius = r) => 2 * r
    case Rect(Depth = d)    => d
}

func main() {
    Println(height(Rect(1, 2)))
}
```

**Error output.**

```
error[GALA-E0073]: 'Rect' has no field 'Depth'
  --> main.gala:10:15
   |
10 |     case Rect(Depth = d)    => d
   |               ^^^^^ its fields are Width, Height
   |
   = hint: its fields are Width, Height
```

**Fix.** Name a field the variant or struct declares, once, after any
positional sub-patterns. Fields left out match anything:

```gala
package main

sealed type Shape {
    case Circle(Radius int)
    case Rect(Width int, Height int)
}

func height(s Shape) int = s match {
    case Circle(Radius = r) => 2 * r
    case Rect(Height = h)   => h
}

func main() {
    Println(height(Rect(1, 2)))
}
```

For an extractor with its own `Unapply`, write the sub-patterns by position:
its result has no field names.

**Rationale.** Named sub-patterns mirror named arguments at construction, so
`Rect(Height = h)` reads the field it names. A name that matches no field, or
a field matched twice, has no meaning to fall back on, so it is an error rather
than a positional guess.
