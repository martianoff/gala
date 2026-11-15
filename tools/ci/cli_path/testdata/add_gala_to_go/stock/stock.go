// Package stock is Go code that was in the module before GALA arrived.
package stock

import (
	"fmt"
	"strconv"
	"strings"
)

// Item is one line of a stock list.
type Item struct {
	SKU string
	Qty int
}

// Parse reads a stock list of "SKU,QTY" lines.
func Parse(text string) ([]Item, error) {
	var items []Item
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		sku, qty, ok := strings.Cut(line, ",")
		if !ok {
			return nil, fmt.Errorf("line %q: want SKU,QTY", line)
		}
		n, err := strconv.Atoi(qty)
		if err != nil {
			return nil, fmt.Errorf("line %q: %w", line, err)
		}
		items = append(items, Item{SKU: sku, Qty: n})
	}
	return items, nil
}
