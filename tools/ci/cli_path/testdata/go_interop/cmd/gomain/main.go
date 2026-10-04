// Command gomain is a Go main package; the GALA code it reaches through
// report is transpiled by `gala build`.
package main

import (
	"fmt"

	"example.com/gointerop/report"
)

func main() {
	fmt.Println(report.Line("a Go main\ncalls GALA\n"))
}
