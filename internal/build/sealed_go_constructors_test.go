package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuild_SealedGoConstructorsFromHandWrittenGo builds a package whose
// hand-written Go builds its sealed type's values with the generated
// constructors, which apply the GALA field defaults a composite literal skips.
func TestBuild_SealedGoConstructorsFromHandWrittenGo(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/goctors", map[string]string{
		"lights/lights.gala": `package lights

sealed type Light {
    case Off()
    default case On(Power int = 30, Room string = "Living")
}
`,
		"lights/describe.go": `package lights

func Describe() string {
	return NewLight().String() + " " + NewLightOn(WithLightOnPower(5)).String() + " " + NewLightOff().String()
}
`,
		"main.gala": `package main

import "example.com/goctors/lights"

func main() {
    Println(lights.Describe())
}
`,
	})
	assert.Equal(t, "On(30,|Living)|On(5,|Living)|Off()", buildAndRun(t, projectDir))
}

// TestBuild_SealedGoConstructorDeclaredInGo: a constructor name a hand-written
// .go file of the package declares is left to it.
func TestBuild_SealedGoConstructorDeclaredInGo(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/goctorsdecl", map[string]string{
		"lights/lights.gala": `package lights

sealed type Light {
    case Off()
    default case On(Power int = 30)
}
`,
		"lights/ctor.go": `package lights

// NewLight starts a Light switched off.
func NewLight() Light { return NewLightOff() }

func Describe() string {
	return NewLight().String() + " " + NewLightOn().String()
}
`,
		"main.gala": `package main

import "example.com/goctorsdecl/lights"

func main() {
    Println(lights.Describe())
}
`,
	})
	assert.Equal(t, "Off()|On(30)", buildAndRun(t, projectDir))
}

// TestBuild_StructGoConstructorAndCasePredicates builds a package whose
// hand-written Go creates its immutable struct with the generated constructor,
// which applies the GALA field defaults, and tests a sealed value's case with
// the generated predicates.
func TestBuild_StructGoConstructorAndCasePredicates(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/gostructctor", map[string]string{
		"cart/cart.gala": `package cart

struct Item(SKU string, Qty int = 1)

sealed type Discount {
    default case NoDiscount()
    case Percent(Pct int64)
}
`,
		"cart/describe.go": `package cart

import "fmt"

func Describe() string {
	a, b := NewItem("A"), NewItem("B", WithItemQty(3))
	d := NewDiscountPercent(10)
	return fmt.Sprintln(a.SKU.Get(), a.Qty.Get(), b.Qty.Get(), d.IsPercent(), d.IsNoDiscount(), NewDiscount().IsNoDiscount())
}
`,
		"main.gala": `package main

import "example.com/gostructctor/cart"

func main() {
    Println(cart.Describe())
}
`,
	})
	assert.Equal(t, "A|1|3|true|false|true", buildAndRun(t, projectDir))
}
