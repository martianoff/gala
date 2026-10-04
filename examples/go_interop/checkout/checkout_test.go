package checkout

import (
	"fmt"

	"martianoff/gala/collection_immutable"
	"martianoff/gala/examples/go_interop/pricing"
)

var cart = []Line{
	{SKU: "A-1", UnitCents: 1000, Qty: 2},
	{SKU: "B-2", UnitCents: 250, Qty: 4},
}

func ExampleTotal() {
	total, err := Total(cart, "PCT10")
	fmt.Println(total, err)

	_, err = Total(cart, "BOGUS")
	fmt.Println(err)

	_, err = Total(nil, "")
	fmt.Println(IsEmptyCart(err))
	// Output:
	// 2700 <nil>
	// unknown coupon BOGUS
	// true
}

func ExampleCouponLabel() {
	for _, code := range []string{"PCT15", "OFF200", "", "FREE"} {
		fmt.Println(CouponLabel(code))
	}
	// Output:
	// 15% off
	// 200 cents off
	// no discount
	// unknown coupon
}

// Example_fromGo builds GALA values directly from Go: a sealed-type case
// through its Apply, an immutable struct through its constructor function,
// an Array from a Go slice, and reads an immutable field with Get.
func Example_fromGo() {
	discount := pricing.Percent{}.Apply(50)
	fmt.Println(discount, discount.Apply(900))

	item := pricing.NewLineItem("C-3", 475, 2)
	fmt.Println(item.SKU.Get(), item.Qty.Get())

	items := collection_immutable.ArrayOf(item, pricing.NewLineItem("D-4", 50, 1))
	fmt.Println(items.Size(), pricing.Subtotal(items))

	receipt := pricing.Checkout(items, "OFF100").Get()
	fmt.Println(receipt.TotalCents, receipt.Lines, receipt.Note)
	// Output:
	// Percent(50) 450
	// C-3 2
	// 2 1000
	// 900 2 AmountOff(100)
}
