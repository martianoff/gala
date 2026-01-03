# GALA Examples

This page contains examples demonstrating various features of the GALA language.

## Complete Example

The following example demonstrates many of GALA's features, including structs, immutability, expression functions, and control flow.

```gala
package main

import "fmt"

struct Point(X, Y int)

func moveX(p Point, delta int) Point = p.Copy(X = p.X + delta)

func main() {
    val p1 = Point(10, 20)
    val p2 = moveX(p1, 5)
    
    val msg = if (p2.X > 10) "moved" else "static"
    fmt.Println(msg, p2)
}
```

## More Examples

You can find more examples in the `examples/` directory of the project:
- `complex.gala`: A more complex example showing pattern matching and `init()` function.
- `hello.gala`: A simple "Hello, World!" example.
- `with_main.gala`: An example with a `main` function.
- `imports.gala`: Demonstrates importing standard Go packages with aliases and dot imports.
- `use_lib.gala`: Demonstrates importing another GALA package (`mathlib`).
