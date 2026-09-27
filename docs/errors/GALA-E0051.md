# GALA-E0051 — Illegal character in source

**When it fires.** The source file contains a character that Go source may not
contain:

- a byte sequence that is not valid UTF-8;
- a NUL character (U+0000);
- a byte order mark (U+FEFF) anywhere except the very start of the file.

The rule is Go's own, and it applies everywhere in the file — inside string,
rune and raw-string literals and inside comments alike. One byte order mark at
the start of a file is still accepted, as in Go; Windows editors often write
one.

**Minimal repro.** These characters are usually invisible in an editor, so the
repro is easiest to describe in words: a string literal holding a raw NUL byte
between `a` and `b`, or a file saved in Latin-1 so that `café` is stored with
the single byte `0xE9`:

```gala
package main

func main() {
    val s = "a<NUL>b"
    Println(s)
}
```

**Error output.** The message names the character; the locus points at it, with
the column counted in characters like every other GALA diagnostic.

```
error[GALA-E0051]: illegal character NUL
error[GALA-E0051]: illegal byte order mark (U+FEFF is only allowed as the first character of a file)
error[GALA-E0051]: invalid UTF-8 encoding (byte 0xE9)
```

Each comes with the hint:

```
= hint: GALA source must be valid UTF-8 without NUL characters or byte order marks, as Go source must; to put such a value in a string, write it as an escape (\x00, \uFEFF, \xFF)
```

**Fix.** Write the value as an escape sequence, which puts exactly the intended
bytes into the string:

```gala
val nul = "a\x00b"      // a, NUL, b
val bom = "\uFEFF"      // the character U+FEFF
val raw = "\xff"        // the single byte 0xFF
```

For invalid UTF-8, re-save the file as UTF-8; the offending byte is usually a
character written in a legacy encoding such as Latin-1 or Windows-1252.

**Rationale.** GALA copies a literal's raw text verbatim into the generated Go,
so a character Go forbids inside a literal used to travel straight into the
emitted `.gen.go`. A NUL or a stray byte order mark made that file unparseable,
which surfaced as an internal transpiler error (GALA-E0017) rather than as a
problem in the user's source. Invalid UTF-8 was worse: the lexer reads its
input as characters, so each invalid byte was silently replaced by U+FFFD and
the program compiled with a different string value than the one in the file.
Checking the source text up front reports all three at the character that
causes them.
