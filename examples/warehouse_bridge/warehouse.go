// Package warehouse_bridge declares Go methods that return several results,
// for checking that GALA lifts a method's results to Try/Tuple the same way
// it lifts a Go function's.
package warehouse_bridge

import "fmt"

// Item is one stock line.
type Item struct {
	SKU string
	Qty int
}

// Inventory holds stock by SKU; its methods have pointer receivers.
type Inventory struct {
	items map[string]Item
}

// New returns an inventory with a little stock in it.
func New() *Inventory {
	return &Inventory{items: map[string]Item{
		"apple": {SKU: "apple", Qty: 5},
		"pear":  {SKU: "pear", Qty: 1},
	}}
}

// Find returns the item and whether it is stocked: (T, bool).
func (inv *Inventory) Find(sku string) (Item, bool) {
	item, ok := inv.items[sku]
	return item, ok
}

// Reserve takes qty of sku out of stock: (T, error).
func (inv *Inventory) Reserve(sku string, qty int) (int, error) {
	item, ok := inv.items[sku]
	if !ok {
		return 0, fmt.Errorf("unknown sku %q", sku)
	}
	if item.Qty < qty {
		return 0, fmt.Errorf("only %d of %s left", item.Qty, sku)
	}
	item.Qty -= qty
	inv.items[sku] = item
	return item.Qty, nil
}

// Check fails when sku is not stocked: an error-only result.
func (inv *Inventory) Check(sku string) error {
	if _, ok := inv.items[sku]; !ok {
		return fmt.Errorf("unknown sku %q", sku)
	}
	return nil
}

// Split returns the two halves of the stock of sku: (A, B).
func (inv *Inventory) Split(sku string) (int, int) {
	q := inv.items[sku].Qty
	return q / 2, q - q/2
}

// Box is a value-receiver type.
type Box struct{ N int }

// Take removes qty from the box: (T, error) on a value receiver.
func (b Box) Take(qty int) (int, error) {
	if qty > b.N {
		return 0, fmt.Errorf("box holds %d, asked for %d", b.N, qty)
	}
	return b.N - qty, nil
}

// FreeReserve is a free function with the same result shape as Reserve.
func FreeReserve(qty int) (int, error) {
	if qty < 0 {
		return 0, fmt.Errorf("negative quantity %d", qty)
	}
	return qty, nil
}
