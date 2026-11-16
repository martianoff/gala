package main

import (
	"fmt"
	"strings"
)

// Order is one customer's purchase.
type Order struct {
	Customer string
	Amount   int
}

func main() {
	orders := []Order{{"ann", 120}, {"bob", 80}, {"ann", 45}, {"cy", 300}, {"bob", 15}}
	var big []Order
	for _, o := range orders {
		if o.Amount >= 50 {
			big = append(big, o)
		}
	}
	total := 0
	for _, o := range big {
		total += o.Amount
	}
	byCustomer := map[string]int{}
	for _, o := range orders {
		byCustomer[o.Customer] += o.Amount
	}
	var bigNames, names []string
	for _, o := range big {
		bigNames = append(bigNames, o.Customer)
	}
	for _, n := range []string{"ann", "bob", "cy"} {
		names = append(names, fmt.Sprintf("%s=%d", n, byCustomer[n]))
	}
	fmt.Printf("big orders: %s\n", strings.Join(bigNames, ", "))
	fmt.Printf("total of big orders: %d\n", total)
	fmt.Printf("per customer: %s\n", strings.Join(names, ", "))
}
