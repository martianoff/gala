// An embedded type of a package the document does not hold (strings, from Go's
// standard library) has methods GoCore does not see: the run stops at the type
// itself (a pointer to it too), after the output before it.
package main

import (
	"fmt"
	"strings"
)

type wrapper struct {
	*strings.Builder
}

func main() {
	fmt.Println("before")
	w := wrapper{&strings.Builder{}}
	fmt.Println(w.Builder == nil)
}
