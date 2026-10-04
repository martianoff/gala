// A main package written only in Go, importing the module's GALA package.
package main

import (
	"fmt"

	"example.com/gocmdmain/textstats"
)

func main() {
	fmt.Printf("gomain: %d words\n", textstats.WordCount("a Go main over GALA"))
}
