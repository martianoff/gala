# GALA-E0038 — Invalid string escape sequence

**When it fires.** A string, rune, interpolated (`s"…"`) or format (`f"…"`)
literal contains an escape sequence that is not one of the escapes GALA accepts.

GALA's accepted set is exactly Go's set for an interpreted literal:

| Escape | Meaning |
|--------|---------|
| `\a` `\b` `\f` `\n` `\r` `\t` `\v` | bell, backspace, form feed, newline, carriage return, tab, vertical tab |
| `\\` | a literal backslash |
| `\"` | a double quote (string literals only) |
| `\'` | a single quote (rune literals only) |
| `\xHH` | one byte, exactly 2 hexadecimal digits |
| `\uHHHH` | a code point, exactly 4 hexadecimal digits |
| `\UHHHHHHHH` | a code point, exactly 8 hexadecimal digits |
| `\OOO` | one byte, exactly 3 octal digits (`0`–`7`), value ≤ 255 |

Anything else after a backslash is rejected. The check also rejects a
well-formed-looking escape whose value is out of range: an octal escape above
255, a `\u`/`\U` escape naming a surrogate half (U+D800–U+DFFF), or a `\U`
escape above U+10FFFF.

Backtick raw strings are **not** affected — they have no escapes at all, so
every backslash in them is already literal.

Rune literals are a narrower case: GALA's lexer admits exactly one character
after the backslash, so only the single-character escapes (`\n`, `\t`, `\\`,
`\'`, …) are writable in a `'x'` literal at all. The numeric forms are a syntax
error there rather than a GALA-E0038 — use a string if you need them.

**Minimal repro.** The usual offender is a regular expression whose backslash
was not doubled:

```gala
package main

func main() {
    val pattern = "(\d{4})"
    Println(pattern)
}
```

**Error output.**

```
error[GALA-E0038]: invalid escape sequence "\d" in string literal
  --> esc.gala:4:21
  |
4 |     val pattern = "(\d{4})"
  |                     ^^ valid escapes are \a \b \f \n \r \t \v \\ \" \xHH \uHHHH \UH…
  |
  = hint: valid escapes are \a \b \f \n \r \t \v \\ \" \xHH \uHHHH \UHHHHHHHH and \OOO (octal); to write a literal backslash (in a regular expression, for example) double it (\\) or use a backtick raw string
```

The caret spans exactly the offending escape sequence.

A malformed — rather than unrecognised — escape names the specific defect:

```
error[GALA-E0038]: invalid escape sequence "\x4" in string literal: \x requires exactly 2 hexadecimal digits
error[GALA-E0038]: invalid escape sequence "\400" in string literal: the octal value 256 is above the maximum byte value 255
error[GALA-E0038]: invalid escape sequence "\uD800" in string literal: a surrogate half (U+D800-U+DFFF) is not a valid Unicode code point
error[GALA-E0038]: invalid escape sequence "\'" in string literal: \' is only valid in a rune literal; inside a string literal write '
```

**Fix.** Double the backslash, or switch to a backtick raw string:

```gala
val pattern = "(\\d{4})"     // escaped: the string holds (\d{4})
val pattern = `(\d{4})`      // raw: every backslash is literal
```

For a regular expression the raw-string form is usually clearer, since a regex
is full of backslashes that would each need doubling.

If you meant a control character, use its named escape (`\n`, `\t`, …) or a
numeric one (`\x0a`, `\012`).

**Rationale.** A literal's raw source text is copied verbatim into the
generated Go literal. GALA's lexer, however, accepts a backslash followed by
*any* character — a strictly wider set than Go's — so an unrecognised escape
used to travel untouched into the emitted `.gen.go`, where it is also invalid.

Nothing downstream caught it. `go/printer` prints a string literal without
parsing it, and the two later passes that *do* re-parse the buffer — the
generator's canonicalising `format.Source` step and the source-map marker
rewrite that lowers internal `__gala_line_N` markers into `//line` directives —
both treated "the buffer does not parse" as tolerable and returned their input
unchanged. The result was a file that still contained the transpiler's internal
markers, emitted with a **success** exit code; the failure only surfaced later
as a Go compiler error about an undefined identifier the user never wrote.

Validating the literal where it is converted turns that whole chain into one
diagnostic pointing at the offending escape in the `.gala` file. The marker
rewrite additionally now fails loudly rather than degrading, so no future
codegen defect can leak internal markers with a success status either.
