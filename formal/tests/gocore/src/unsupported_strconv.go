// A failing strconv.Atoi builds its *NumError with internal/stringslite.Clone,
// a package GoCore does not hold: the run stops there, after the output before
// it (Go prints "0 strconv.Atoi: parsing \"x\": invalid syntax").
package main

import (
	"fmt"
	"strconv"
)

func main() {
	fmt.Println("before")
	n, err := strconv.Atoi("x")
	fmt.Println(n, err)
}
