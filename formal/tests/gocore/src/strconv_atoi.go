// strconv.Atoi, run from Go's own strconv/atoi.go, quote.go and isprint.go
// (gocoreexport.RunnableStdlib): the fast path for short inputs, signs, int64's
// bounds through ParseInt, and the errors, printed with Quote.
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
	for _, s := range []string{"x", "", "-", "1_000", "99999999999999999999", "12a", "héllo\n"} {
		n, err := strconv.Atoi(s)
		fmt.Println(n, err)
	}
}
