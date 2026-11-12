# GALA-E0100 — Type does not implement the interface it is used as

**When it fires.** A value of a concrete type fills a place typed by an
interface, and the type does not implement that interface under Go's rules.
The place can be an argument, a `val` with a declared type, a result, a struct
field, a collection element or an assignment. The message names the first
method that fails and why:

- the type has no such method;
- the method takes other parameter types than the interface declares;
- the method returns another type than the interface declares;
- the method has a pointer receiver, so only a pointer to the value has it.

**Minimal repro.**

```gala
package main

type Shape interface {
    Area() float64
    Scale(k float64) Shape
}

struct Square(side float64)

func (s Square) Area() float64 = s.side * s.side
func (s Square) Scale(k float64) Square = Square(s.side * k)

func total(s Shape) float64 = s.Area()

func main() {
    Println(total(Square(2.0)))
}
```

**Error output.**

```
error[GALA-E0100]: Square does not implement Shape: method Scale returns Square, not Shape
  --> main.gala:16:19
   |
16 |     Println(total(Square(2.0)))
   |                   ^^^^^^^^^^^ declare Scale with the interface's exact signature
   |
   = hint: declare Scale with the interface's exact signature
```

**Fix.** Declare the method with the exact signature the interface declares:

```gala
package main

type Shape interface {
    Area() float64
    Scale(k float64) Shape
}

struct Square(side float64)

func (s Square) Area() float64 = s.side * s.side
func (s Square) Scale(k float64) Shape = Square(s.side * k)

func total(s Shape) float64 = s.Area()

func main() {
    Println(total(Square(2.0)))
}
```

For a method on a pointer receiver, pass a pointer to a `var` (`&counter`), or
declare the method on a value receiver.

**Rationale.** GALA interfaces are Go interfaces, and Go has no covariant
result types: `Scale(k float64) Square` does not implement
`Scale(k float64) Shape`, even though a `Square` is a `Shape`. The method's
signature must be identical to the interface's. This used to pass every GALA
check and then fail in `go build` with a "does not implement" error against
the generated code.
