// strconv.Atoi, run from Go's own strconv/atoi.go (gocoreexport.RunnableStdlib):
// the fast path for short inputs, signs, and int64's bounds through ParseInt.
package main

import (
	"fmt"
	"strconv"
)

func main() {
	for _, s := range []string{"0", "42", "-7", "+15", "007", "-0",
		"9223372036854775807", "-9223372036854775808", "123456789012345678"} {
		n, err := strconv.Atoi(s)
		fmt.Println(n, err == nil)
	}
	fmt.Println(strconv.IntSize)
}
