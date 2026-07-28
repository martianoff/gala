---
layout: default
title: "Regex in GALA — Regular Expressions with Pattern Matching Extractors"
description: "GALA's Regex package provides regular expression support with pattern matching extractors and Array destructuring. Type-safe regex with Try-based compilation and functional operations."
keywords: "gala regex, golang regex alternative, go regex pattern matching, gala regular expressions, go regex extractor, gala regex destructuring"
permalink: /docs/regex/
last_modified_at: 2026-04-19
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Regex</p>

# Regex — Regular Expressions with Pattern Matching

GALA's `regex` package wraps Go's `regexp` with a functional API and pattern matching extractors. Capture groups destructure directly into variables inside `match` expressions — no manual group indexing.

```gala
import "martianoff/gala/regex"
import . "martianoff/gala/std"
import . "martianoff/gala/collection_immutable"
```

---

## Compilation

```gala
// Safe compilation — returns Try[Regex]
val r = regex.Compile("\\d+")
r.ForEach((re) => Println(re.Matches("abc123")))

// Panicking compilation — for known-good patterns
val digits = regex.MustCompile("\\d+")
```

Invalid patterns return `Failure` instead of panicking:

```gala
val bad = regex.Compile("[invalid")
Println(bad.IsFailure())  // true
```

---

## Matching and Searching

```gala
val digits = regex.MustCompile("\\d+")

digits.Matches("abc123")            // true
digits.Matches("no digits")         // false

digits.FindFirst("abc 123 def")     // Some("123")
digits.FindAll("a1 b2 c3")         // Array("1", "2", "3")
```

---

## Replace and Split

```gala
val digits = regex.MustCompile("\\d+")

digits.ReplaceAll("Call 555-1234", "***")   // "Call ***-***"
digits.Split("a1b2c3")                      // Array("a", "b", "c")
```

---

## Capture Groups

`FindGroups` returns the first match with all capture groups:

```gala
val dateRegex = regex.MustCompile("(\\d{4})-(\\d{2})-(\\d{2})")
val groups = dateRegex.FindGroups("2024-01-15")
// Some(Array("2024-01-15", "2024", "01", "15"))
```

---

## Pattern Matching with Array Destructuring

The real power of GALA's regex is in `match` expressions. The `Unapply` method extracts capture groups (excluding the full match) and returns them as an `Array`. Combined with Array sequence patterns, each group destructures into its own variable:

```gala
val dateRegex = regex.MustCompile("(\\d{4})-(\\d{2})-(\\d{2})")

val result = "2024-01-15" match {
    case dateRegex(Array(year, month, day)) => s"$year-$month-$day"
    case _ => "not a date"
}
// "2024-01-15"
```

### Email Extraction

```gala
val emailRegex = regex.MustCompile("([\\w.]+)@([\\w.]+)")

val parts = "user@example.com" match {
    case emailRegex(Array(user, domain)) => s"User: $user, Domain: $domain"
    case _ => "not an email"
}
// "User: user, Domain: example.com"
```

### Multiple Patterns

Combine regex extractors with other match patterns:

```gala
val dateRegex = regex.MustCompile("(\\d{4})-(\\d{2})-(\\d{2})")
val timeRegex = regex.MustCompile("(\\d{2}):(\\d{2}):(\\d{2})")

func parseTimestamp(s string) string = s match {
    case dateRegex(Array(y, m, d)) => s"Date: $y/$m/$d"
    case timeRegex(Array(h, m, sec)) => s"Time: $h:$m:$sec"
    case _ => "unknown format"
}
```

---

## API Reference

| Method | Description |
|--------|-------------|
| <code>regex.Compile(pattern)</code> | Safe compilation, returns <code>Try[Regex]</code> |
| <code>regex.MustCompile(pattern)</code> | Panicking compilation |
| <code>Matches(s)</code> | Test if string matches |
| <code>FindFirst(s)</code> | First match as <code>Option[string]</code> |
| <code>FindAll(s)</code> | All matches as <code>Array[string]</code> |
| <code>FindGroups(s)</code> | First match with groups as <code>Option[Array[string]]</code> |
| <code>ReplaceAll(s, replacement)</code> | Replace all matches |
| <code>Split(s)</code> | Split string by pattern |
| <code>Unapply(s)</code> | Pattern matching extractor, returns <code>Option[Array[string]]</code> |

---

## Further Reading

- [Pattern Matching](/features/pattern-matching/) — exhaustive matching with destructuring and guards
- [Json](/docs/json/) — JSON parsing with pattern matching extractors
- [Error Handling](/features/error-handling/) — Try-based error handling
