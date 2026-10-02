package analyzer

import "testing"

const packageValSrc = `package pv

struct Box(V int)

struct Adder(N int)

func (a Adder) Apply(x int) int = a.N + x

func setup() {
}

func count() int = 3

val I = 3
val Neg = -3
val F = 1.5
val S = "x"
val Interp = s"v=$I"
val R = 'r'
val B = true
val Eq = s"$S" == ""
val StrEq = "a" == "b"
val Ref = I
val MkBox = Box(1)
val AddTen = Adder(10)
val N = count()
val Void = setup()
var VoidVar = setup()
val (TupA, TupB) = (1, "b")
var (TupC, TupD) = (2, "d")
`

// TestPackageValInitTypes: the element type a package-level binding records
// for each initializer shape. A comparison is not its operands' type, and a
// void call has no type — it must be recorded as NilType, never a nil
// interface.
func TestPackageValInitTypes(t *testing.T) {
	rich := analyzeSrc(t, packageValSrc)
	tests := []struct{ name, want string }{
		{"I", "int"},
		{"Neg", "int"},
		{"F", "float64"},
		{"S", "string"},
		{"Interp", "string"},
		{"R", "rune"},
		{"B", "bool"},
		{"Ref", "int"},
		{"MkBox", "pv.Box"},
		{"AddTen", "pv.Adder"},
		{"N", "int"},
		{"Eq", ""},
		{"StrEq", ""},
		{"Void", ""},
		{"VoidVar", ""},
	}
	for _, tc := range tests {
		pv := rich.PackageVals[tc.name]
		if pv == nil {
			t.Fatalf("PackageVals missing %s", tc.name)
		}
		if pv.Type == nil {
			t.Fatalf("%s: Type is a nil interface", tc.name)
		}
		got := pv.Type.String()
		if pv.Type.IsNil() {
			got = ""
		}
		if got != tc.want {
			t.Errorf("%s: type = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestVoidPackageValSurvivesCache: a void initializer's NilType round-trips
// through the cache codec as NilType, so an importer served from the cache
// never meets a nil Type.
func TestVoidPackageValSurvivesCache(t *testing.T) {
	rich := analyzeSrc(t, packageValSrc)
	blob, err := encodeCachedRichAST(&CachedRichAST{PackageName: "pv", PackageVals: rich.PackageVals})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeCachedRichAST(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, name := range []string{"Void", "VoidVar"} {
		pv := got.PackageVals[name]
		if pv == nil || pv.Type == nil || !pv.Type.IsNil() {
			t.Fatalf("%s after round-trip = %+v, want a NilType entry", name, pv)
		}
		if pv.Name != name {
			t.Errorf("%s: decoded Name = %q", name, pv.Name)
		}
	}
}

// TestPackageTupleDestructuringIsRecorded: every name a package-level tuple
// destructuring binds is a package val or var, so a reference from another file
// of the package unwraps a val's Immutable and leaves a var as it is.
func TestPackageTupleDestructuringIsRecorded(t *testing.T) {
	rich := analyzeSrc(t, packageValSrc)
	for name, isVal := range map[string]bool{"TupA": true, "TupB": true, "TupC": false, "TupD": false} {
		pv := rich.PackageVals[name]
		if pv == nil {
			t.Fatalf("PackageVals missing %s", name)
		}
		if pv.IsVal != isVal {
			t.Errorf("%s: IsVal = %v, want %v", name, pv.IsVal, isVal)
		}
		if pv.Type == nil || !pv.Type.IsNil() {
			t.Errorf("%s: Type = %v, want NilType", name, pv.Type)
		}
	}
}
