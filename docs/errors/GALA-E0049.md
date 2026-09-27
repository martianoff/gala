# GALA-E0049 — Go multi-value call where one value is needed

**When it fires.** A call to a Go function or method that returns two or more
values stands where GALA needs exactly one value:

```gala
os.ReadFile(path) match {           // match subject
    case (data, nil) => ...
}
val (n, err) = strconv.Atoi(text)   // tuple destructuring
val c = strings.Cut(line, "=")      // one name, no error result
val r = if (ok) strconv.Atoi(a) else strconv.Atoi(b)   // if-expression branch
val f = () => strings.Cut(line, "=")                   // lambda body, no error result
takesPair(strings.Cut(line, "="))                      // Tuple parameter
twoArgs("a", strconv.Atoi(text))                       // one argument of several
```

**Minimal repro.**

```gala
package main

import "os"

func describe(path string) string = os.ReadFile(path) match {
    case (_, nil) => "read"
    case _ => "failed"
}

func main() {
    Println(describe("missing.txt"))
}
```

**Error output.**

```
error[GALA-E0049]: os.ReadFile returns 2 values, but a match subject takes a single value
  --> main.gala:5:37
  |
5 | func describe(path string) string = os.ReadFile(path) match {
  |                                     ^^^^^^^^^^^^^^^^^ wrap it in `Try(os.ReadFile(...))` and match on `Success(v)`…
  |
  = hint: wrap it in `Try(os.ReadFile(...))` and match on `Success(v)` / `Failure(e)`, or bind the results with `val a, b = os.ReadFile(...)`
```

**Fix.** A Go multi-return is not a Tuple, and GALA has no multi-value
expressions, so the results are reached in one of three documented ways.

A `(T, error)` or `(A, B, error)` call goes through `Try`, which turns the
error into `Failure` and carries two or more values as a Tuple:

```gala
func describe(path string) string = Try(os.ReadFile(path)) match {
    case Success(_) => "read"
    case Failure(err) => s"failed: ${err.Error()}"
}
```

Any multi-value call can bind its results by name — without parentheses — and
a Tuple built from those names can then be matched:

```gala
func setting(line string) string {
    val key, value, found = strings.Cut(line, "=")
    return (key, value, found) match {
        case (k, v, true) => s"$k -> $v"
        case _ => s"$key has no value"
    }
}
```

A single name over a `(T, error)` call takes the value and panics on the error;
the same holds for an expression lambda whose body is such a call. Neither is
rejected:

```gala
val n = strconv.Atoi("21")
```

**Rationale.** The call was emitted verbatim and the transformer typed it as
its first result. Matched against a tuple pattern, the subject became a
`[]byte` with `.V1` / `.V2` fields read off it, and the failure came from the
Go compiler, naming generated code:

```
main.gala:5: obj.V1 undefined (type []byte has no field or method V1)
```

Tuple destructuring, if-expression branches, lambda bodies and Tuple-typed
arguments failed the same way, as `multiple-value ... in single-value context`
or `too many return values`. Converting the results into a Tuple silently was
not an option: a single name over a `(T, error)` call already means "the value,
or panic", so the same call would have meant two different things depending on
where it was written.

**Where it stands down.** A multi-value call that is the *sole* argument of a
call is left to Go, which spreads it over the parameters when their count
matches (`fmt.Println(strconv.Atoi("7"))` prints `7 <nil>`) and reports the
mismatch otherwise. `Try(call)`,
`val a, b = call`, and a single name or expression-lambda body over a
`(T, error)` call are the documented forms and are untouched.

**Scope.** This code covers Go calls with two or more results. A Go call whose
only result is an `error`, discarded in a void lambda, is reported separately
with a hint naming `FromError`. The `if` initializer
(`if n, err := strconv.Atoi(s); ...`) is [GALA-E0047](GALA-E0047.md).
