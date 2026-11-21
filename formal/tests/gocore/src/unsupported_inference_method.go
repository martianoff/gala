// Go infers U through C's method (Go 1.21: a type argument's methods unify with its
// constraint's) before an untyped constant's default, so 7 here is an int64. GoCore
// does not model that step: a parameter some constraint mentions is never defaulted,
// and it stops. go run prints "before", then "int64 7".
package main

import "fmt"

type Big struct{}

func (Big) Get() int64 { return 1 }

func f[U any, C interface{ Get() U }](c C, u U) string { return fmt.Sprintf("%T %v", u, u) }

func main() {
	fmt.Println("before")
	fmt.Println(f(Big{}, 7))
}
