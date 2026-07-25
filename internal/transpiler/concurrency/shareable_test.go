package concurrency

import (
	"testing"

	"martianoff/gala/internal/transpiler"
)

// --- test helpers ------------------------------------------------------------

// stubResolver is a MetadataResolver backed by a map keyed the same way the
// analyzer keys richAST.Types: "Package.Name" for package-qualified types and
// bare "Name" for main/test-package types.
func stubResolver(types map[string]*transpiler.TypeMetadata) MetadataResolver {
	return func(named transpiler.Type) (*transpiler.TypeMetadata, bool) {
		key := named.BaseName() // BasicType -> Name, NamedType -> "Pkg.Name"
		m, ok := types[key]
		return m, ok
	}
}

// convenience constructors mirroring how the analyzer builds transpiler.Type
// values (see resolveTypeWithParams / resolveBaseName).
func basic(name string) transpiler.Type { return transpiler.BasicType{Name: name} }

func named(pkg, name string) transpiler.Type {
	return transpiler.NamedType{Package: pkg, Name: name}
}

func generic(base transpiler.Type, params ...transpiler.Type) transpiler.Type {
	return transpiler.GenericType{Base: base, Params: params}
}

// immutable/mutable collection constructors.
func immArray(elem transpiler.Type) transpiler.Type {
	return generic(named(pkgCollectionImmutable, "Array"), elem)
}
func immList(elem transpiler.Type) transpiler.Type {
	return generic(named(pkgCollectionImmutable, "List"), elem)
}
func immHashMap(k, v transpiler.Type) transpiler.Type {
	return generic(named(pkgCollectionImmutable, "HashMap"), k, v)
}
func immHashSet(elem transpiler.Type) transpiler.Type {
	return generic(named(pkgCollectionImmutable, "HashSet"), elem)
}
func mutArray(elem transpiler.Type) transpiler.Type {
	return generic(named(pkgCollectionMutable, "Array"), elem)
}

// std wrappers.
func option(t transpiler.Type) transpiler.Type {
	return generic(named("std", transpiler.TypeOption), t)
}
func try(t transpiler.Type) transpiler.Type {
	return generic(named("std", transpiler.TypeTry), t)
}
func either(a, b transpiler.Type) transpiler.Type {
	return generic(named("std", transpiler.TypeEither), a, b)
}
func tuple2(a, b transpiler.Type) transpiler.Type {
	return generic(named("std", transpiler.TypeTuple), a, b)
}
func immutableW(t transpiler.Type) transpiler.Type {
	return generic(named("std", transpiler.TypeImmutable), t)
}
func constPtr(t transpiler.Type) transpiler.Type {
	return generic(named("std", transpiler.TypeConstPtr), t)
}

// --- metadata fixtures -------------------------------------------------------

// A struct whose fields are all shareable `val`s.
//
//	type Point struct { val x int; val y int }
var pointMeta = &transpiler.TypeMetadata{
	Name:       "Point",
	FieldNames: []string{"x", "y"},
	Fields:     map[string]transpiler.Type{"x": basic("int"), "y": basic("int")},
	ImmutFlags: []bool{true, true},
}

// A struct with a mutable `var` field.
//
//	type Counter struct { var n int }
var counterMeta = &transpiler.TypeMetadata{
	Name:       "Counter",
	FieldNames: []string{"n"},
	Fields:     map[string]transpiler.Type{"n": basic("int")},
	ImmutFlags: []bool{false},
}

// A struct holding an immutable-collection field (still shareable).
//
//	type Config struct { val items Array[string] }
var configMeta = &transpiler.TypeMetadata{
	Name:       "Config",
	Package:    "app",
	FieldNames: []string{"items"},
	Fields:     map[string]transpiler.Type{"items": immArray(basic("string"))},
	ImmutFlags: []bool{true},
}

// A struct with a Go-interop field (opaque, not shareable).
//
//	type Handle struct { val f os.File }   // os.File unresolved -> opaque
var handleMeta = &transpiler.TypeMetadata{
	Name:       "Handle",
	Package:    "app",
	FieldNames: []string{"f"},
	Fields:     map[string]transpiler.Type{"f": named("os", "File")},
	ImmutFlags: []bool{true},
}

// A struct with a mutable-collection field (not shareable).
//
//	type Bag struct { val data collection_mutable.Array[int] }
var bagMeta = &transpiler.TypeMetadata{
	Name:       "Bag",
	Package:    "app",
	FieldNames: []string{"data"},
	Fields:     map[string]transpiler.Type{"data": mutArray(basic("int"))},
	ImmutFlags: []bool{true},
}

// A generic struct: type Box[T] struct { val v T }
var boxMeta = &transpiler.TypeMetadata{
	Name:       "Box",
	Package:    "app",
	TypeParams: []string{"T"},
	FieldNames: []string{"v"},
	Fields:     map[string]transpiler.Type{"v": basic("T")},
	ImmutFlags: []bool{true},
}

// A sealed type whose variants are all shareable.
//
//	sealed Shape:  Circle(val r float64) | Square(val side int)
var shapeMeta = &transpiler.TypeMetadata{
	Name:     "Shape",
	Package:  "app",
	IsSealed: true,
	SealedVariants: []transpiler.SealedVariant{
		{Name: "Circle", FieldNames: []string{"r"}, FieldTypes: []transpiler.Type{basic("float64")}},
		{Name: "Square", FieldNames: []string{"side"}, FieldTypes: []transpiler.Type{basic("int")}},
	},
}

// A sealed type with a mutable-collection field in one variant (not shareable).
//
//	sealed Event: Tick | Batch(val items collection_mutable.Array[int])
var eventMeta = &transpiler.TypeMetadata{
	Name:     "Event",
	Package:  "app",
	IsSealed: true,
	SealedVariants: []transpiler.SealedVariant{
		{Name: "Tick"},
		{Name: "Batch", FieldNames: []string{"items"}, FieldTypes: []transpiler.Type{mutArray(basic("int"))}},
	},
}

// A recursive immutable cons list: type IntList sealed  Nil | Cons(val head int, val tail IntList)
var intListMeta = &transpiler.TypeMetadata{
	Name:     "IntList",
	Package:  "app",
	IsSealed: true,
	SealedVariants: []transpiler.SealedVariant{
		{Name: "Nil"},
		{Name: "Cons", FieldNames: []string{"head", "tail"},
			FieldTypes: []transpiler.Type{basic("int"), named("app", "IntList")}},
	},
}

// A recursive struct that carries a mutable field: should terminate and be false.
//
//	type Node struct { val next Node; var seen bool }
var nodeMeta = &transpiler.TypeMetadata{
	Name:       "Node",
	Package:    "app",
	FieldNames: []string{"next", "seen"},
	Fields:     map[string]transpiler.Type{"next": named("app", "Node"), "seen": basic("bool")},
	ImmutFlags: []bool{true, false},
}

func fixtureResolver() MetadataResolver {
	return stubResolver(map[string]*transpiler.TypeMetadata{
		"Point":        pointMeta,
		"Counter":      counterMeta,
		"app.Config":   configMeta,
		"app.Handle":   handleMeta,
		"app.Bag":      bagMeta,
		"app.Box":      boxMeta,
		"app.Shape":    shapeMeta,
		"app.Event":    eventMeta,
		"app.IntList":  intListMeta,
		"app.Node":     nodeMeta,
	})
}

// --- the table ---------------------------------------------------------------

func TestIsShareable(t *testing.T) {
	c := NewChecker(fixtureResolver())

	tests := []struct {
		name string
		typ  transpiler.Type
		want bool
	}{
		// primitives (all shareable)
		{"int", basic("int"), true},
		{"int8", basic("int8"), true},
		{"int16", basic("int16"), true},
		{"int32", basic("int32"), true},
		{"int64", basic("int64"), true},
		{"uint", basic("uint"), true},
		{"uint8", basic("uint8"), true},
		{"uint16", basic("uint16"), true},
		{"uint32", basic("uint32"), true},
		{"uint64", basic("uint64"), true},
		{"float32", basic("float32"), true},
		{"float64", basic("float64"), true},
		{"complex64", basic("complex64"), true},
		{"complex128", basic("complex128"), true},
		{"bool", basic("bool"), true},
		{"string", basic("string"), true},
		{"byte", basic("byte"), true},
		{"rune", basic("rune"), true},
		{"unit/void", transpiler.VoidType{}, true},
		{"Unit-as-name", basic("Unit"), true},

		// interfaces / escape hatches are NOT primitives
		{"any not shareable", basic("any"), false},
		{"error not shareable", basic("error"), false},
		{"uintptr not shareable", basic("uintptr"), false},

		// immutable collections — shareable iff args shareable
		{"Array[int]", immArray(basic("int")), true},
		{"List[string]", immList(basic("string")), true},
		{"HashSet[int]", immHashSet(basic("int")), true},
		{"HashMap[string,int]", immHashMap(basic("string"), basic("int")), true},

		// mutable collections — never shareable
		{"mutable Array[int]", mutArray(basic("int")), false},

		// nested collections
		{"List[HashMap[string, Array[int]]]",
			immList(immHashMap(basic("string"), immArray(basic("int")))), true},
		{"List[MutableArray[int]]",
			immList(mutArray(basic("int"))), false},
		{"HashMap[string, MutableArray[int]]",
			immHashMap(basic("string"), mutArray(basic("int"))), false},

		// Option / Try / Either / tuple
		{"Option[int]", option(basic("int")), true},
		{"Option[MutableArray[int]]", option(mutArray(basic("int"))), false},
		{"Try[string]", try(basic("string")), true},
		{"Try[MutableArray[int]]", try(mutArray(basic("int"))), false},
		{"Either[int,string]", either(basic("int"), basic("string")), true},
		{"Either[int,MutableArray]", either(basic("int"), mutArray(basic("int"))), false},
		{"Tuple2[int,string]", tuple2(basic("int"), basic("string")), true},
		{"Tuple2[int,MutableArray]", tuple2(basic("int"), mutArray(basic("int"))), false},

		// Immutable / ConstPtr wrappers (conditional on payload — see deviation note)
		{"Immutable[int]", immutableW(basic("int")), true},
		{"Immutable[Array[int]]", immutableW(immArray(basic("int"))), true},
		{"Immutable[MutableArray[int]]", immutableW(mutArray(basic("int"))), false},
		{"ConstPtr[int]", constPtr(basic("int")), true},
		{"ConstPtr[MutableArray[int]]", constPtr(mutArray(basic("int"))), false},

		// structs
		{"struct all-val-shareable", basic("Point"), true},
		{"struct with var field", basic("Counter"), false},
		{"struct with immutable-collection field", named("app", "Config"), true},
		{"struct with Go-interop field", named("app", "Handle"), false},
		{"struct with mutable-collection field", named("app", "Bag"), false},

		// generic struct instantiations
		{"Box[int]", generic(named("app", "Box"), basic("int")), true},
		{"Box[MutableArray[int]]", generic(named("app", "Box"), mutArray(basic("int"))), false},

		// sealed types
		{"sealed all-shareable", named("app", "Shape"), true},
		{"sealed with mutable variant field", named("app", "Event"), false},

		// recursion
		{"recursive immutable list terminates+shareable", named("app", "IntList"), true},
		{"recursive struct with var field terminates+false", named("app", "Node"), false},

		// function types — always false (capture analysis handles closures later)
		{"func type", transpiler.FuncType{Params: []transpiler.Type{basic("int")}, Results: []transpiler.Type{basic("int")}}, false},

		// bare type parameter — false conservatively
		{"bare type param T", basic("T"), false},

		// bare Go slice / map / pointer — false
		{"go slice []int", transpiler.ArrayType{Elem: basic("int")}, false},
		{"go map[string]int", transpiler.MapType{Key: basic("string"), Elem: basic("int")}, false},
		{"go pointer *int", transpiler.PointerType{Elem: basic("int")}, false},

		// opaque Go-interop named type (unresolved) — false
		{"opaque go type", named("os", "File"), false},

		// nil / unresolved — false, no panic
		{"NilType", transpiler.NilType{}, false},
		{"nil interface", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.IsShareable(tc.typ); got != tc.want {
				t.Errorf("IsShareable(%v) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

// TestIsShareableNilResolver documents the resolver-free convenience: it decides
// value-kinded types correctly but conservatively refuses user structs it cannot
// resolve.
func TestIsShareableNilResolver(t *testing.T) {
	cases := []struct {
		name string
		typ  transpiler.Type
		want bool
	}{
		{"primitive still shareable", basic("int"), true},
		{"immutable collection still shareable", immArray(basic("int")), true},
		{"mutable collection still not", mutArray(basic("int")), false},
		{"Option[int] still shareable", option(basic("int")), true},
		{"unresolved struct -> false", basic("Point"), false},
		{"func type -> false", transpiler.FuncType{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsShareable(tc.typ); got != tc.want {
				t.Errorf("IsShareable(%v) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

// TestSubstitute pins the type-parameter substitution used for generic structs.
func TestSubstitute(t *testing.T) {
	subst := buildSubst([]string{"T"}, []transpiler.Type{basic("int")})

	got := substitute(immArray(basic("T")), subst)
	want := immArray(basic("int"))
	if got.String() != want.String() {
		t.Errorf("substitute Array[T] = %s, want %s", got.String(), want.String())
	}

	// identity when no subst
	id := substitute(immArray(basic("T")), nil)
	if id.String() != immArray(basic("T")).String() {
		t.Errorf("nil subst should be identity, got %s", id.String())
	}
}
