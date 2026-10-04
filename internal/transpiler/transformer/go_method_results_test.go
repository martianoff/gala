package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// warehouseGo is a hand-written Go package inside the module whose methods
// return several results.
const warehouseGo = `package warehouse

type Item struct {
	SKU string
	Qty int
}

type Inventory struct{ items map[string]Item }

func New() *Inventory { return &Inventory{items: map[string]Item{}} }

func (inv *Inventory) Find(sku string) (Item, bool) {
	item, ok := inv.items[sku]
	return item, ok
}

func (inv *Inventory) Reserve(sku string, qty int) (int, error) { return qty, nil }

type Box struct{ N int }

func (b Box) Take(qty int) (int, error) { return qty, nil }
`

// TestGoMethodMultiResultsLifted covers a METHOD of a type declared in a Go
// package of the module: its several results are one GALA value, Try[T] for
// (T, error) and Tuple[A, B] for (A, B), exactly as for a Go function.
func TestGoMethodMultiResultsLifted(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		expect []string // text the generated Go must contain
	}{
		{
			name: "value receiver (T, error) as a match subject",
			body: `func take(b warehouse.Box) string = b.Take(1) match {
    case Success(left) => s"ok $left"
    case Failure(_) => "bad"
}`,
			expect: []string{"GoTry(b.Take(1))"},
		},
		{
			name: "pointer receiver (T, error) bound then matched",
			body: `func reserve(inv *warehouse.Inventory) string {
    val r = inv.Reserve("x", 1)
    r match {
        case Success(left) => s"ok $left"
        case Failure(_) => "bad"
    }
}`,
			expect: []string{"GoTry(inv.Reserve(\"x\", 1))"},
		},
		{
			name: "pointer receiver (T, bool) off a constructor, destructured",
			body: `func find() bool {
    val (item, found) = warehouse.New().Find("x")
    found && item.Qty > 0
}`,
			expect: []string{"GoTuple(warehouse.New().Find(\"x\"))"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nimport \"example.com/gomethods/warehouse\"\n\n" + tc.body + "\n\nfunc main() {}\n"
			goCode, err := transpileInModule(t, map[string]string{
				"go.mod":                 "module example.com/gomethods\n\ngo 1.25\n",
				"gala.mod":               "module example.com/gomethods\n",
				"warehouse/warehouse.go": warehouseGo,
				"main.gala":              src,
			}, "main.gala")
			require.NoError(t, err)
			for _, text := range tc.expect {
				assert.Contains(t, goCode, text, "generated Go:\n%s", goCode)
			}
		})
	}
}

// ownGo is the hand-written Go file of a mixed package: its functions return
// several results, one of them a type the package declares in GALA.
const ownGo = `package mixed

import "errors"

func CountInt() (int, error) { return 3, nil }

func IntAndFlag() (int, bool) { return 4, true }

func MakeBox() (Box, error) {
	if false {
		return Box{}, errors.New("no box")
	}
	return Box{N: 5}, nil
}
`

// TestOwnGoFuncMultiResultsLifted covers a mixed package's GALA code calling
// a function its own .go file declares: several results are one GALA value,
// as for a function of an imported Go package.
func TestOwnGoFuncMultiResultsLifted(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		expect []string
	}{
		{
			name: "(T, error) as a match subject",
			body: `func count() int = CountInt() match {
    case Success(n) => n
    case Failure(_) => 0
}`,
			expect: []string{"GoTry(CountInt())"},
		},
		{
			name:   "(T, error) bound, then a Try method",
			body:   "func count() int {\n    val r = CountInt()\n    r.GetOrElse(0)\n}",
			expect: []string{"GoTry(CountInt())"},
		},
		{
			name:   "(A, B) destructured",
			body:   "func flagged() bool {\n    val (n, ok) = IntAndFlag()\n    ok && n > 0\n}",
			expect: []string{"GoTuple(IntAndFlag())"},
		},
		{
			name:   "(GALA type, error) mapped",
			body:   "func boxed() int = MakeBox().Map((b) => b.N).GetOrElse(0)",
			expect: []string{"GoTry(MakeBox())"},
		},
		{
			name:   "names bound one by one keep the raw results",
			body:   "func count() int {\n    val n, err = CountInt()\n    if (err != nil) 0 else n\n}",
			expect: []string{"CountInt()"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package mixed\n\nstruct Box(var N int)\n\n" + tc.body + "\n"
			goCode, err := transpileInModule(t, map[string]string{
				"go.mod":         "module example.com/ownfuncs\n\ngo 1.25\n",
				"gala.mod":       "module example.com/ownfuncs\n",
				"mixed/own.go":   ownGo,
				"mixed/box.gala": src,
			}, "mixed/box.gala")
			require.NoError(t, err)
			for _, text := range tc.expect {
				assert.Contains(t, goCode, text, "generated Go:\n%s", goCode)
			}
		})
	}
}
