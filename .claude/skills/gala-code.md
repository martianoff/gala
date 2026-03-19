---
description: Generate GALA code with tests. TRIGGER when user asks to write, create, generate, or implement something in GALA, or asks to code a feature, module, library, or program in the GALA language.
user-invocable: true
---

# GALA Code Generator

Generate production-quality GALA code with comprehensive tests following GALA best practices.

**Argument:** `$ARGUMENTS` - Description of what to build (e.g., "a stack data structure", "a calculator", "a URL parser")

## Instructions

### Step 0: Research GALA Syntax

Read `docs/GALA.MD` to ensure you use correct GALA syntax and idioms. Pay special attention to:
- Variable declarations (`val` vs `var`)
- Expression-bodied functions (`func f() T = expr`)
- Pattern matching (`match`, sealed types, extractors)
- Lambda syntax (`(x) => expr`)
- Struct declarations (shorthand `struct Name(field Type)` vs block `type Name struct { ... }`)
- Collection types (`Array`, `List`, `HashMap` from `collection_immutable`)
- Standard library types (`Option`, `Either`, `Try`, `Tuple`)

Also read the test framework: `test/framework.gala`, `test/assertions.gala`, `test/table.gala` for test patterns.

If the code you are generating needs to **import a package** or **produce a new package**, also read `C:\Users\maxmr\GolandProjects\gala_simple\docs\DEPENDENCY_MANAGEMENT.MD` for correct dependency declaration, Bazel `deps`, and import path conventions.

### Step 1: Design the GALA Code

Design the implementation following GALA best practices:

1. **Immutability first** - Use `val` by default, `var` only when truly needed
2. **Sealed types for variants** - Use `sealed type` instead of manual discriminators, iota enums, or interface unions
3. **Pattern matching over if-else** - Use `match` for Option, Either, sealed types, and multi-branch logic
4. **Functional patterns** - Prefer `Map`, `Filter`, `FoldLeft`, `ForEach` over manual loops
5. **GALA collections** - Use `Array`, `List`, `HashMap` from `collection_immutable` (not Go slices unless for Go interop)
6. **Variadic args → Array** - Convert variadic params to GALA collections with `ArrayOf(args...)` for functional processing (FoldLeft, Map, Filter). Don't write manual loops over Go slices when a functional pattern fits
7. **Expression-bodied functions** - Use `func f() T = expr` for single-expression functions
8. **Implicit typing** - Omit type params where inferable: `Some(42)` not `Some[int](42)`, `(x) => x * 2` not `(x int) => x * 2`
9. **Generics over reflection** - Use type parameters for reusable code
10. **Copy for updates** - Use `.Copy(field = newValue)` instead of mutation
11. **Default parameters** - Use default values for optional params: `func connect(host string, port int = 8080)`. Callers can omit trailing defaults or use named args: `connect("localhost", tls = false)`
12. **Named arguments** - Use named args for clarity: `divide(dividend = 20, divisor = 4)`. Works with any GALA function, not just structs
13. **String interpolation** - Use `s"Hello $name"` instead of `fmt.Sprintf("Hello %s", name)`. Use `f"$x%.2f"` for explicit format control. No `import "fmt"` needed.
14. **Println/Print** - Use `Println(...)` and `Print(...)` instead of `fmt.Println(...)` / `fmt.Print(...)`. No import needed.
15. **Try for Go errors** - Wrap Go functions that return `(T, error)` with `Try(f)` when f takes no args, or `Try(() => f(args))` when args are needed. Use `.OrElse` for independent fallbacks, `.FlatMap` for dependent chains. Never use sequential `if err == nil` blocks.
16. **Option over sentinels** - Return `Option[T]` instead of sentinel values (`""`, `0`, `-1`, `nil`) to signal absence. Use `.GetOrElse`, `.Map`, or `match` on the caller side.

### Step 2: Write the Source Code

Create the GALA source file(s). Follow these conventions:

- Package declaration first, followed by blank line
- Imports next, followed by blank line
- Type definitions, then functions/methods
- Use meaningful names: PascalCase for types and exports, camelCase for locals

**File location**: Place new libraries under a descriptive directory (e.g., `examples/myfeature/` for standalone examples, or a new package directory for libraries).

**Import patterns**:
```gala
// Standard library types (Option, Try, etc.) - auto-available via std
// Println, Print — auto-available, no import needed
// String interpolation (s"...", f"...") — auto-available, no import needed
// GALA collections
import . "martianoff/gala/collection_immutable"
// Go interop (SliceOf, MapEmpty, etc.)
import . "martianoff/gala/go_interop"
// Test framework
import . "martianoff/gala/test"
// Go standard library (only when needed for functions beyond Println/Print/Sprintf)
import "strings"
```

**String formatting** — prefer interpolation over `fmt.Sprintf`:
```gala
// GOOD: string interpolation (no import needed)
val msg = s"Hello $name, you are $age years old"
val formatted = f"Price: $$$price%.2f"
Println(s"Result: $x")

// AVOID: fmt.Sprintf (requires import "fmt")
val msg = fmt.Sprintf("Hello %s, you are %d years old", name, age)
fmt.Println(fmt.Sprintf("Result: %d", x))
```

### Step 3: Write Tests

Create a test file using `gala_go_test` conventions. The Bazel macro auto-generates the `main()` function that discovers and runs all `Test*` functions - you do NOT need to write `main()` or `RunTests()` yourself.

**Test file rules:**
1. **Package**: `package main`
2. **Import**: `. "martianoff/gala/test"` and the module under test
3. **Test functions**: `func TestXxx(t T) T` - take `T`, return `T`
4. **No main()**: The `gala_go_test` macro auto-generates it
5. **Assertions**: Use `Eq`, `IsTrue`, `IsFalse`, `Greater`, `Less`, `Contains`, `IsSome`, `IsNone`, `IsSuccess`, `IsFailure`, `Panics`, etc.
6. **Chained assertions**: Pass `T` through assertion chain: `var t1 = Eq(t, a, b); return IsTrue(t1, c)`
7. **Subtests**: Use `t.Run("name", (sub T) => ...)` for logical grouping
8. **Table-driven tests**: Use `RunCases[In, Out](t, func, cases...)` for parameterized tests

**Test file template**:
```gala
package main

import (
    . "martianoff/gala/test"
    . "martianoff/gala/mypackage"
)

func TestBasic(t T) T {
    val result = MyFunction(42)
    return Eq(t, result, expected)
}

// Table-driven test
func TestTable(t T) T {
    return RunCases[int, int](t,
        (sub T, input int, expected int) => Eq(sub, Transform(input), expected),
        Case[int, int](Name = "case1", Input = 1, Expected = 2),
        Case[int, int](Name = "case2", Input = 5, Expected = 10),
    )
}

// Subtests
func TestSubtests(t T) T {
    var t1 = t.Run("happy path", (sub T) => Eq(sub, Compute(1), 1))
    return t1.Run("edge case", (sub T) => Eq(sub, Compute(0), 0))
}
```

### Step 4: Create BUILD.bazel

Add Bazel build configuration.

**For a library + test**:
```python
load("//:gala.bzl", "gala_go_test", "gala_library")

gala_library(
    name = "mylib",
    srcs = ["mylib.gala"],
    importpath = "martianoff/gala/mylib",
    visibility = ["//visibility:public"],
)

gala_go_test(
    name = "mylib_test",
    srcs = ["mylib_test.gala"],
    deps = [":mylib"],
)
```

**For multi-file packages**:
```python
gala_library(
    name = "mypackage",
    srcs = ["types.gala", "ops.gala"],
    importpath = "martianoff/gala/mypackage",
    visibility = ["//visibility:public"],
)
```

**For standalone example (output comparison, not test framework)**:
```python
load("//:gala.bzl", "gala_test")

gala_test(
    name = "myexample",
    src = "myexample.gala",
    expected = "myexample.out",
)
```

### Step 5: Run GALA Lint

After writing the code, invoke the `/gala-lint` skill on the generated files to verify they follow GALA best practices. Fix any HIGH or MEDIUM severity issues found.

### Step 6: Build and Test

Run the following commands to verify everything works:

1. `bazel run //:gazelle` - regenerate BUILD files if needed
2. `bazel build //...` - verify compilation
3. `bazel test //...` - run all tests (including the new ones)

Fix any compilation errors or test failures before considering the task complete.

## Quality Checklist

Before finishing, verify:

- [ ] All variables use `val` unless mutation is required
- [ ] Pattern matching used instead of if-else chains on Option/Either/sealed types
- [ ] Sealed types used instead of manual discriminators or iota enums
- [ ] GALA collections (`Array`, `List`) used instead of Go slices for internal logic
- [ ] Variadic args converted with `ArrayOf(args...)` when functional processing is needed
- [ ] Expression-bodied functions used for single-expression functions
- [ ] Default parameter values used for optional parameters instead of overloads/options pattern
- [ ] Named arguments used at call sites when it improves readability
- [ ] Type parameters omitted where inferable
- [ ] String interpolation (`s"..."` / `f"..."`) used instead of `fmt.Sprintf`
- [ ] `Println`/`Print` used instead of `fmt.Println`/`fmt.Print`
- [ ] No `import "fmt"` unless using functions beyond Println/Print/Sprintf (e.g., `fmt.Errorf`)
- [ ] Go error returns wrapped with `Try(f)` (zero-arg) or `Try(() => f(args))`, not `if err == nil` blocks
- [ ] Fallback patterns use `Try.OrElse`, not sequential if-err-nil
- [ ] No sentinel return values (`""`, `0`, `-1`) — use `Option[T]` instead
- [ ] Tests cover happy path, edge cases, and error cases
- [ ] Table-driven tests used for parameterized scenarios
- [ ] Test file has NO `main()` function (auto-generated by `gala_go_test`)
- [ ] `bazel build //...` passes
- [ ] `bazel test //...` passes
- [ ] GALA lint reports no HIGH severity issues
