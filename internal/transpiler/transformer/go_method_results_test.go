package transformer_test

import (
	"strings"
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

func (inv *Inventory) Check(sku string) error { return nil }

type Box struct{ N int }

func (b Box) Take(qty int) (int, error) { return qty, nil }
`

// TestGoMethodMultiResultsLifted covers a METHOD of a type declared in a Go
// package of the module: its several results are one GALA value, Try[T] for
// (T, error) and Tuple[A, B] for (A, B), exactly as for a Go function.
func TestGoMethodMultiResultsLifted(t *testing.T) {
	cases := []liftCase{
		{
			name: "value receiver (T, error) as a match subject",
			body: `func take(b warehouse.Box) string = b.Take(1) match {
    case Success(left) => s"ok $left"
    case Failure(_) => "bad"
}`,
			want: "GoTry(b.Take(1))",
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
			want: "GoTry(inv.Reserve(\"x\", 1))",
		},
		{
			name: "pointer receiver (T, bool) off a constructor, destructured",
			body: `func find() bool {
    val (item, found) = warehouse.New().Find("x")
    found && item.Qty > 0
}`,
			want: "GoTuple(warehouse.New().Find(\"x\"))",
		},
		{
			name: "error-only method in a Try fails on the error",
			body: `func check(inv *warehouse.Inventory) bool = Try(() => inv.Check("x")).IsSuccess()`,
			want: "_err := inv.Check(\"x\"); _err != nil",
		},
	}
	runLiftCases(t, cases, func(body string) (map[string]string, string) {
		return map[string]string{
			"go.mod":                 "module example.com/gomethods\n\ngo 1.25\n",
			"gala.mod":               "module example.com/gomethods\n",
			"warehouse/warehouse.go": warehouseGo,
			"main.gala":              "package main\n\nimport \"example.com/gomethods/warehouse\"\n\n" + body + "\n\nfunc main() {}\n",
		}, "main.gala"
	})
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
	cases := []liftCase{
		{
			name: "(T, error) as a match subject",
			body: `func count() int = CountInt() match {
    case Success(n) => n
    case Failure(_) => 0
}`,
			want: "GoTry(CountInt())",
		},
		{
			name: "(T, error) bound, then a Try method",
			body: "func count() int {\n    val r = CountInt()\n    r.GetOrElse(0)\n}",
			want: "GoTry(CountInt())",
		},
		{
			name: "(A, B) destructured",
			body: "func flagged() bool {\n    val (n, ok) = IntAndFlag()\n    ok && n > 0\n}",
			want: "GoTuple(IntAndFlag())",
		},
		{
			name: "(GALA type, error) mapped",
			body: "func boxed() int = MakeBox().Map((b) => b.N).GetOrElse(0)",
			want: "GoTry(MakeBox())",
		},
		{
			name:     "names bound one by one keep the raw results",
			body:     "func count() int {\n    val n, err = CountInt()\n    if (err != nil) 0 else n\n}",
			want:     "CountInt()",
			unlifted: true,
		},
		{
			name:     "a local binding of the name is the callee",
			body:     "func count() int {\n    val CountInt = () => 7\n    val r = CountInt()\n    r + 1\n}",
			want:     "CountInt.Get()()",
			unlifted: true,
		},
	}
	runLiftCases(t, cases, func(body string) (map[string]string, string) {
		return map[string]string{
			"go.mod":         "module example.com/ownfuncs\n\ngo 1.25\n",
			"gala.mod":       "module example.com/ownfuncs\n",
			"mixed/own.go":   ownGo,
			"mixed/box.gala": "package mixed\n\nstruct Box(var N int)\n\n" + body + "\n",
		}, "mixed/box.gala"
	})
}

// TestSameNamedGoImportIsNotOwnPackage covers a GALA package importing a Go
// package of its own name: Go type info files both under that name, so a bare
// call of the package's GALA function must not take the import's Go
// signature of the same name.
func TestSameNamedGoImportIsNotOwnPackage(t *testing.T) {
	cases := []liftCase{
		{
			name:     "GALA function in a Try thunk",
			body:     "func total() int =Try(() => Count()).GetOrElse(0) + om.Other()",
			want:     "Count()",
			unlifted: true,
		},
	}
	runLiftCases(t, cases, func(body string) (map[string]string, string) {
		return map[string]string{
			"go.mod":             "module example.com/samename\n\ngo 1.25\n",
			"gala.mod":           "module example.com/samename\n",
			"lib/mixed/mixed.go": "package mixed\n\nfunc Count() (int, error) { return 1, nil }\n\nfunc Other() int { return 2 }\n",
			"mixed/box.gala":     "package mixed\n\nimport om \"example.com/samename/lib/mixed\"\n\nfunc Count() int = 3\n\n" + body + "\n",
		}, "mixed/box.gala"
	})
}

// liftCase is a GALA function body and text its generated Go must contain.
// An unlifted case's call must also not be wrapped in GoTry/GoTuple, nor run
// as a Go call whose error a Try thunk panics on.
type liftCase struct {
	name     string
	body     string
	want     string
	unlifted bool
}

// runLiftCases transpiles each case's body inside the module files that
// module returns, with the GALA file to transpile.
func runLiftCases(t *testing.T, cases []liftCase, module func(body string) (map[string]string, string)) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, entry := module(tc.body)
			goCode, err := transpileInModule(t, files, entry)
			require.NoError(t, err)
			assert.Contains(t, goCode, tc.want, "generated Go:\n%s", goCode)
			if tc.unlifted {
				assert.False(t, strings.Contains(goCode, "GoTry(") || strings.Contains(goCode, "GoTuple(") || strings.Contains(goCode, "_err"),
					"raw results must not be lifted; generated Go:\n%s", goCode)
			}
		})
	}
}
