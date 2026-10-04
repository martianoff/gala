// Package warehouse is ordinary hand-written Go that the Go interop guide
// (docs/GO_INTEROP.MD) calls from GALA. It uses the shapes a Go API tends to
// have: a struct with exported fields, pointer-receiver methods, a
// (T, bool) lookup, (T, error) and error-only results, a slice result, a
// callback parameter and an interface parameter.
package warehouse

import (
	"errors"
	"fmt"
	"sort"
)

// ErrOutOfStock is returned by Reserve when there is not enough stock.
var ErrOutOfStock = errors.New("out of stock")

// Item is one stock-keeping unit.
type Item struct {
	SKU   string
	Name  string
	Qty   int
	Price float64
}

// Inventory is a mutable, map-backed store.
type Inventory struct {
	items map[string]Item
}

// New returns an empty inventory.
func New() *Inventory {
	return &Inventory{items: map[string]Item{}}
}

// Add stores an item, replacing any item with the same SKU.
func (inv *Inventory) Add(item Item) {
	inv.items[item.SKU] = item
}

// Find looks an item up by SKU.
func (inv *Inventory) Find(sku string) (Item, bool) {
	item, ok := inv.items[sku]
	return item, ok
}

// Reserve takes qty units of sku out of stock and returns what is left.
func (inv *Inventory) Reserve(sku string, qty int) (int, error) {
	item, ok := inv.items[sku]
	if !ok {
		return 0, fmt.Errorf("unknown SKU %q", sku)
	}
	if item.Qty < qty {
		return item.Qty, ErrOutOfStock
	}
	item.Qty -= qty
	inv.items[sku] = item
	return item.Qty, nil
}

// Remove deletes an item; it fails when the SKU is unknown.
func (inv *Inventory) Remove(sku string) error {
	if _, ok := inv.items[sku]; !ok {
		return fmt.Errorf("unknown SKU %q", sku)
	}
	delete(inv.items, sku)
	return nil
}

// Items returns every item, sorted by SKU.
func (inv *Inventory) Items() []Item {
	out := make([]Item, 0, len(inv.items))
	for _, item := range inv.items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKU < out[j].SKU })
	return out
}

// Predicate decides whether an item is selected.
type Predicate func(Item) bool

// Select returns the items, sorted by SKU, that keep accepts.
func (inv *Inventory) Select(keep Predicate) []Item {
	var out []Item
	for _, item := range inv.Items() {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}

// Notifier receives a message for every low-stock item.
type Notifier interface {
	Notify(message string)
}

// CheckLowStock notifies n of every item with fewer than threshold units and
// returns how many it reported.
func (inv *Inventory) CheckLowStock(threshold int, n Notifier) int {
	count := 0
	for _, item := range inv.Items() {
		if item.Qty < threshold {
			n.Notify(fmt.Sprintf("%s is low: %d left", item.SKU, item.Qty))
			count++
		}
	}
	return count
}
