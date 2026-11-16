// errors.Is is in wrap.go, a file of package errors GoCore does not run: it
// reports the call, never gets stuck. go run prints "before", then "true".
package main

import (
	"errors"
	"fmt"
)

func main() {
	e := errors.New("x")
	fmt.Println("before")
	fmt.Println(errors.Is(e, e))
}
