// A panic with a value whose type has String prints that String.
package main

import "fmt"

type E struct{ code int }

func (e E) String() string { return fmt.Sprintf("code %d", e.code) }

func main() {
	fmt.Println("start")
	panic(E{7})
}
