// Package checkout is hand-written Go that calls the GALA package pricing.
// It shows, from the Go side, how GALA's structs, sealed types, Option, Try
// and immutable collections look (docs/GO_INTEROP.MD, part 2).
package checkout

import (
	"errors"
	"fmt"

	"martianoff/gala/collection_immutable"
	"martianoff/gala/examples/go_interop/pricing"
	"martianoff/gala/std"
)

// Line is the Go-side input: a plain Go struct.
type Line struct {
	SKU       string
	UnitCents int64
	Qty       int
}

// toItems converts Go values into the immutable Array the GALA API takes.
func toItems(lines []Line) collection_immutable.Array[pricing.LineItem] {
	items := make([]pricing.LineItem, 0, len(lines))
	for _, l := range lines {
		items = append(items, pricing.NewLineItem(l.SKU, l.UnitCents, pricing.WithLineItemQty(l.Qty)))
	}
	return collection_immutable.ArrayFromSlice(items)
}

// Total runs the GALA checkout and turns its Try into Go's (value, error).
func Total(lines []Line, coupon string) (int64, error) {
	result := pricing.Checkout(toItems(lines), coupon)
	if result.IsFailure() {
		return 0, result.GetError()
	}
	return result.Get().TotalCents, nil
}

// Describe reads a GALA sealed value from Go: IsPercent and IsAmountOff test
// its case, and the case's Unapply returns its fields.
func Describe(d pricing.Discount) string {
	switch {
	case d.IsPercent():
		return fmt.Sprintf("%d%% off", pricing.Percent{}.Unapply(d).Get())
	case d.IsAmountOff():
		return fmt.Sprintf("%d cents off", pricing.AmountOff{}.Unapply(d).Get())
	}
	return "no discount"
}

// CouponLabel calls a GALA function that returns an Option and maps it with
// a Go func. Generic methods such as Option.Map are free functions in Go.
func CouponLabel(code string) string {
	label := std.Option_Map(pricing.ParseCoupon(code), Describe)
	return label.GetOrElse("unknown coupon")
}

// IsEmptyCart tests a GALA-declared Go error value with errors.Is.
func IsEmptyCart(err error) bool {
	return errors.Is(err, pricing.ErrEmptyCart)
}
