// Go's own source of errors.New and unicode/utf8.RuneCountInString, which
// GoCore runs as it is (gocoreexport.RunnableStdlib).
package main

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

var errBoom = errors.New("boom")

func find(ok bool) error {
	if ok {
		return nil
	}
	return errBoom
}

func main() {
	err := find(false)
	fmt.Println(err, err == errBoom, find(true) == nil)
	// Each New is a new error, though the text is the same.
	fmt.Println(errors.New("x") == errors.New("x"))
	fmt.Printf("[%8v] [%s]\n", errBoom, errors.New("hé"))
	for _, s := range []string{"", "go", "héllo", "日本語", "aé\U0001F600"} {
		fmt.Println(utf8.RuneCountInString(s), len(s))
	}
}
