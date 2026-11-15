// Command inventory is the module's Go main package. It calls the GALA code
// through ordinary Go imports.
package main

import (
	"fmt"

	"github.com/google/uuid"

	"example.com/inventory/report"
	"example.com/inventory/stock"
)

const list = "A-1,12\nB-2,3\nC-3,0"

func main() {
	// A GALA function in the existing Go package stock.
	items, err := stock.Parse(list)
	if err != nil {
		panic(err)
	}
	fmt.Println("reorder:", stock.Reorder(items, 5))

	// A GALA function that declares Go's (string, error) result list.
	line, err := report.Describe(list, 5)
	fmt.Println(line, err)
	_, err = report.Describe("A-1", 5)
	fmt.Println("error:", err)

	// A GALA function returning Try[Summary]: Go tests it and reads the value.
	result := report.Build(list, 5)
	if result.IsSuccess() {
		summary := result.Get()
		fmt.Println(summary.Units.Get(), summary.Reorder.Get().MkString("+"))
	}

	// A Go dependency that go.mod requires.
	fmt.Println(uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8").Version())
}
