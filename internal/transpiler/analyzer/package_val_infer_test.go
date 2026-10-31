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
val (_, TupE) = (3, "e")
`

// TestPackageValInitTypes: the element type a package-level binding records
// for each initializer shape. A comparison is a bool, not its operands' type,
// and a void call has no type — it must be recorded as NilType, never a nil
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
		{"Eq", "bool"},
		{"StrEq", "bool"},
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
// of the package unwraps a val's Immutable and leaves a var as it is, and each
// name takes its component's type.
func TestPackageTupleDestructuringIsRecorded(t *testing.T) {
	rich := analyzeSrc(t, packageValSrc)
	if _, ok := rich.PackageVals["_"]; ok {
		t.Errorf("the blank identifier is recorded as a package val")
	}
	tests := []struct {
		name, typ string
		isVal     bool
	}{
		{"TupA", "int", true},
		{"TupB", "string", true},
		{"TupC", "int", false},
		{"TupD", "string", false},
		{"TupE", "string", true},
	}
	for _, tc := range tests {
		pv := rich.PackageVals[tc.name]
		if pv == nil {
			t.Fatalf("PackageVals missing %s", tc.name)
		}
		if pv.IsVal != tc.isVal {
			t.Errorf("%s: IsVal = %v, want %v", tc.name, pv.IsVal, tc.isVal)
		}
		if pv.Type == nil || pv.Type.String() != tc.typ {
			t.Errorf("%s: Type = %v, want %s", tc.name, pv.Type, tc.typ)
		}
	}
}

// TestNumericLiteralType: a numeric literal is read as gala.g4 lexes one,
// signed or not, in every base. An identifier is no literal, even one named
// like an exponent (`E+1`), and a hex digit E is no exponent (`0xE`).
func TestNumericLiteralType(t *testing.T) {
	tests := []struct{ text, want string }{
		{"3", "int"},
		{"-3", "int"},
		{"+3", "int"},
		{"0x10", "int"},
		{"-0x10", "int"},
		{"0xE", "int"},
		{"-0xe", "int"},
		{"-0b101", "int"},
		{"+0o17", "int"},
		{"0644", "int"},
		{"1.5", "float64"},
		{"-1.", "float64"},
		{"-.5", "float64"},
		{"1e3", "float64"},
		{"-1E-3", "float64"},
		{"1.5e+2", "float64"},
		{"E+1", ""},
		{"E-1", ""},
		{"e", ""},
		{"E", ""},
		{"1+2", ""},
		{"1-e", ""},
		{"--1", ""},
		{"-", ""},
		{"", ""},
		{".", ""},
		{"0x", ""},
		{"0b2", ""},
	}
	for _, tc := range tests {
		if got := numericLiteralType(tc.text); got != tc.want {
			t.Errorf("numericLiteralType(%q) = %q, want %q", tc.text, got, tc.want)
		}
		if got := inferLiteralType(tc.text); got != tc.want {
			t.Errorf("inferLiteralType(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// TestDefaultLiteralWidening: a numeric literal default fits every parameter
// type Go converts the untyped constant to, so a hex default for a uintptr
// or complex parameter — now read as an int literal — is not rejected.
func TestDefaultLiteralWidening(t *testing.T) {
	tests := []struct {
		param, literal string
		want           bool
	}{
		{"uintptr", "int", true},
		{"complex64", "int", true},
		{"complex128", "int", true},
		{"complex128", "float64", true},
		{"float32", "int", true},
		{"uint8", "int", true},
		{"int", "float64", false},
		{"uintptr", "float64", false},
		{"string", "int", false},
		{"bool", "float64", false},
	}
	for _, tc := range tests {
		if got := typesCompatibleForDefault(tc.param, tc.literal); got != tc.want {
			t.Errorf("typesCompatibleForDefault(%q, %q) = %v, want %v", tc.param, tc.literal, got, tc.want)
		}
	}
}
